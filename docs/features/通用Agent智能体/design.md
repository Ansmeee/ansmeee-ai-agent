# 通用 Agent 智能体 — 技术方案设计

## 1. 概述

本文档基于[产品需求文档](proposal.md)，给出通用 Agent 智能体的详细技术方案。

### 1.1 设计目标

- 在现有架构（DI + Channel 流式 + GORM + LangChainGo）上做最小侵入式改造
- Engine 从"纯流式转发"升级为"ReAct 推理循环"
- 工具系统从"已注册但未执行"升级为"按 Agent 配置动态绑定并自主调用"
- SSE 协议向后兼容（新事件类型 + 旧客户端可忽略）

### 1.2 影响范围

| 模块 | 变更类型 | 说明 |
|------|---------|------|
| `internal/agent/engine.go` | 重构 | ProcessStream 增加 ReAct 循环 |
| `internal/agent/callback.go` | 扩展 | 新增 OnToolStart/OnToolEnd（已有接口，补充调用） |
| `internal/agent/store.go` | 扩展 | Create/Update 支持新字段 |
| `internal/llm/provider.go` | 扩展 | Chat 方法完善工具 Schema 传递 |
| `internal/tool/registry.go` | 扩展 | 新增 GetByNames、Schema 获取方法 |
| `internal/tool/*.go` | 扩展 | 各工具实现 Parameters() 方法 |
| `internal/memory/interface.go` | 不变 | Message 结构保持 Role + Content 两字段不变（工具元信息存于 content JSON） |
| `internal/memory/mysql.go` | 扩展 | 支持 role=3/4 的存取 |
| `internal/models/agent.go` | 扩展 | 新增 Tools/ModelConfig 等字段 |
| `internal/models/chat_message.go` | 扩展 | 新增 role 常量 |
| `internal/handler/stream.go` | 扩展 | 处理新 StreamEvent 类型 |
| `internal/handler/agent.go` | 扩展 | CRUD 支持新字段 |
| `internal/config/config.go` | 扩展 | 新增 Agent 配置段 |
| 数据库 DDL | 变更 | ai_agent 表新增 4 个字段 |

---

## 2. 整体架构

### 2.1 改造后的请求流

```
POST /api/v1/chat/completion
    │
    ▼
StreamHandler.Handle()
    │  ← 解析请求、设置 SSE headers
    │  ← 发送 session 事件（session_id）
    │  ← 从 AgentStore 获取 Agent 完整配置（含 tools/model_config/max_iterations）
    │  ← 检查 Agent status（禁用则返回错误）
    │  ← 从 ModelConfigStore 获取用户模型配置
    │  ← 合并模型配置（优先级：Agent > 用户 > 系统默认）
    │
    ▼
Engine.ProcessStream(ctx, sessionID, userMsg, agentConfig, mergedModelCfg, userID)
    │
    ▼
┌─────────────────── ReAct 循环 ───────────────────┐
│                                                   │
│  1. 保存用户消息 → 获取历史 → 构建消息列表          │
│  2. 从 Registry 按 agentConfig.Tools 过滤工具      │
│  3. for iter := 0; iter < maxIter; iter++          │
│       │                                            │
│       ├─ emit StreamEvent{type: "thinking"}        │
│       ├─ llmProvider.Chat(messages, filteredTools)  │
│       │                                            │
│       ├─ if ToolCalls 为空:                         │
│       │    ├─ 将 Chat 返回的文本按 chunk 发送        │
│       │    ├─ emit StreamEvent{type: "chunk"} ...   │
│       │    └─ break                                │
│       │                                            │
│       └─ if ToolCalls 非空:                         │
│            ├─ emit StreamEvent{type: "tool_start"} │
│            ├─ 根据 parallel_tool_calls 串行/并行执行 │
│            ├─ emit StreamEvent{type: "tool_end"}   │
│            ├─ 追加 tool 消息到 messages              │
│            └─ continue                             │
│                                                   │
│  4. 保存完整对话链到 Memory                         │
│  5. emit StreamEvent{type: "done"}                 │
└───────────────────────────────────────────────────┘
    │
    ▼
StreamHandler: 遍历 channel → writeSSE() → Flush
```

### 2.2 LLM 调用策略

ReAct 循环中统一使用**非流式调用（Chat）**，LLM 每轮返回完整 response 后：

- **有 ToolCalls** → 执行工具 → 继续循环
- **无 ToolCalls** → 直接取 `result.Content`，按固定大小（如 4 字符）拆分为 chunk 发送，模拟流式效果

```
循环第 1 轮: Chat(with tools) → 返回 ToolCalls → 执行工具 → 继续
循环第 2 轮: Chat(with tools) → 返回 ToolCalls → 执行工具 → 继续
循环第 3 轮: Chat(with tools) → 无 ToolCalls → 直接将 result.Content 拆分为 chunk 发送
```

这样设计的原因：
- LangChainGo 的 `StreamChat` 无法同时获取 ToolCalls（流式回调按 chunk 推送，ToolCall 在完整 response 里）
- 中间轮次无需流式（用户看到的是 `tool_start`/`tool_end` 事件）
- 最终回答直接复用 Chat 的结果，避免二次调用 LLM 造成 token 浪费和延迟翻倍
- 拆分 chunk 发送可保留前端打字机渲染效果（chunk 间以微小延迟发送）

> **Trade-off**: 相比真正的流式输出，用户感知到的首字节时间略长（需等 LLM 完整返回），但节省了一次完整 LLM 调用的 token 开销和延迟。对于大多数 Agent 场景（回答长度适中），体验差异可忽略。

---

## 3. 数据模型变更

### 3.1 ai_agent 表

新增字段：

```sql
ALTER TABLE ai_agent ADD COLUMN tools JSON DEFAULT NULL COMMENT '启用的工具名称列表，如 ["calculator","datetime"]';
ALTER TABLE ai_agent ADD COLUMN model_config JSON DEFAULT NULL COMMENT '模型参数覆盖 {"model":"","temperature":0.7,"max_tokens":4096,"top_p":1.0}';
ALTER TABLE ai_agent ADD COLUMN max_iterations TINYINT NOT NULL DEFAULT 5 COMMENT '最大推理迭代次数';
ALTER TABLE ai_agent ADD COLUMN status TINYINT NOT NULL DEFAULT 1 COMMENT '状态: 1=启用 2=禁用';
```

对应 GORM Model 变更：

```go
// internal/models/agent.go

type Agent struct {
    ID            int64            `json:"-" gorm:"primaryKey;autoIncrement;column:id"`
    UUID          string           `json:"id" gorm:"column:uuid;type:char(36);uniqueIndex;not null;default:''"`
    UserID        int64            `json:"user_id" gorm:"column:user_id;not null;default:0;index"`
    Title         string           `json:"title" gorm:"column:title;type:varchar(255);not null;default:''"`
    Description   string           `json:"description" gorm:"column:intro;type:varchar(1000);not null;default:''"`
    Prompt        string           `json:"prompt" gorm:"column:prompt;type:text"`
    Tools         JSONStringSlice  `json:"tools" gorm:"column:tools;type:json"`
    ModelConfig   *AgentModelConfig `json:"model_config" gorm:"column:model_config;type:json"`
    MaxIterations int8             `json:"max_iterations" gorm:"column:max_iterations;type:tinyint;not null;default:5"`
    Status        int8             `json:"status" gorm:"column:status;type:tinyint;not null;default:1"`
    UpdatedAt     time.Time        `json:"updated_at" gorm:"column:mtime;autoUpdateTime"`
    CreatedAt     time.Time        `json:"created_at" gorm:"column:ctime;autoCreateTime"`
}

// AgentModelConfig 内嵌的模型参数覆盖
// 注意：Temperature 和 TopP 使用 *float64 指针类型，避免 omitempty 吞掉合法的 0 值
type AgentModelConfig struct {
    Model       string   `json:"model,omitempty"`
    Temperature *float64 `json:"temperature,omitempty"`
    MaxTokens   int      `json:"max_tokens,omitempty"`
    TopP        *float64 `json:"top_p,omitempty"`
}

// JSONStringSlice 实现 JSON 数组 <-> []string 的 GORM 序列化
type JSONStringSlice []string
// 需实现 driver.Valuer 和 sql.Scanner 接口

// Agent 状态常量
const (
    AgentStatusEnabled  int8 = 1 // 启用
    AgentStatusDisabled int8 = 2 // 禁用
)
```

### 3.2 ai_agent_template 表（预留，对应 US-8）

为 Agent 模板功能预留数据模型，Phase 4 实现：

```go
// internal/models/agent_template.go

type AgentTemplate struct {
    ID          int64            `json:"-" gorm:"primaryKey;autoIncrement"`
    UUID        string           `json:"id" gorm:"column:uuid;type:char(36);uniqueIndex;not null"`
    Name        string           `json:"name" gorm:"column:name;type:varchar(255);not null"`
    Category    string           `json:"category" gorm:"column:category;type:varchar(100);not null"`
    Description string           `json:"description" gorm:"column:description;type:varchar(1000)"`
    Prompt      string           `json:"prompt" gorm:"column:prompt;type:text"`
    Tools       JSONStringSlice  `json:"tools" gorm:"column:tools;type:json"`
    ModelConfig *AgentModelConfig `json:"model_config" gorm:"column:model_config;type:json"`
    MaxIterations int8           `json:"max_iterations" gorm:"column:max_iterations;type:tinyint;not null;default:5"`
    SortOrder   int              `json:"sort_order" gorm:"column:sort_order;not null;default:0"`
    CreatedAt   time.Time        `json:"created_at" gorm:"column:ctime;autoCreateTime"`
}
```

预置模板示例（通过 seed 数据插入）：

| 模板名 | Category | Tools | Temperature |
|--------|----------|-------|-------------|
| 通用助手 | general | `["calculator","datetime"]` | 0.7 |
| 数学助手 | math | `["calculator"]` | 0.3 |
| 信息查询 | search | `["websearch","weather","datetime"]` | 0.5 |

用户创建 Agent 时可选择模板，前端将模板的 Prompt/Tools/ModelConfig 预填到创建表单中。

### 3.3 ai_chat_session_history 表

无需 DDL 变更（`role` 字段为 tinyint，新增 role 值即可）。

新增 role 常量：

```go
// internal/models/chat_message.go

const (
    RoleUser              int8 = 1 // 用户消息
    RoleAssistant         int8 = 2 // AI 文本回复
    RoleTool              int8 = 3 // 工具执行结果
    RoleAssistantToolCall int8 = 4 // AI 工具调用请求
)
```

Role=4 的消息 content 字段存储 JSON：

```json
{
  "tool_calls": [
    {"id": "call_abc", "name": "calculator", "arguments": "{\"expression\":\"2^10\"}"}
  ]
}
```

Role=3 的消息 content 字段存储 JSON：

```json
{
  "tool_call_id": "call_abc",
  "name": "calculator",
  "result": "1024"
}
```

---

## 4. 模块详细设计

### 4.1 Agent Engine 改造

#### 4.1.1 StreamEvent 扩展

```go
// internal/agent/engine.go

type StreamEvent struct {
    Type       string `json:"type"`       // "chunk" | "done" | "error" | "tool_start" | "tool_end" | "thinking"
    Content    string `json:"content"`
    Error      error  `json:"-"`

    // 工具调用字段（仅 tool_start/tool_end 使用）
    ToolCallID string `json:"tool_call_id,omitempty"`
    ToolName   string `json:"tool_name,omitempty"`
    Arguments  string `json:"arguments,omitempty"`
    Result     string `json:"result,omitempty"`
    Success    bool   `json:"success,omitempty"`

    // 思考轮次（仅 thinking 使用）
    Iteration  int    `json:"iteration,omitempty"`
}
```

#### 4.1.2 AgentConfig 参数

ProcessStream 的签名改为接收结构化的 Agent 配置，而非裸 promptOverride 字符串：

```go
// AgentConfig 聚合了一次推理需要的 Agent 级配置
type AgentConfig struct {
    Prompt             string
    Tools              []string // 允许使用的工具名列表，空=全部
    MaxIterations      int      // 最大推理迭代次数
    ParallelToolCalls  *bool    // 是否允许并行工具调用，nil=使用系统默认
    ModelConfig        *models.AgentModelConfig
}

func (e *Engine) ProcessStream(
    ctx context.Context,
    sessionID string,
    userMessage string,
    agentCfg *AgentConfig,
    userModelCfg *models.ModelConfig,
    userID int64,
) <-chan StreamEvent
```

#### 4.1.3 ReAct 循环核心逻辑

```go
func (e *Engine) ProcessStream(...) <-chan StreamEvent {
    ch := make(chan StreamEvent, 20)

    go func() {
        defer close(ch)
        defer func() {
            if r := recover(); r != nil {
                ch <- StreamEvent{Type: "error", Error: fmt.Errorf("panic: %v", r)}
            }
        }()

        // 1. 确定 system prompt
        prompt := e.systemPrompt
        if agentCfg != nil && agentCfg.Prompt != "" {
            prompt = agentCfg.Prompt
        }

        // 2. 确定最大迭代次数
        maxIter := e.maxIter
        if agentCfg != nil && agentCfg.MaxIterations > 0 {
            maxIter = agentCfg.MaxIterations
        }

        // 3. 解析可用工具列表
        filteredTools := e.resolveTools(agentCfg)

        // 4. 合并模型配置，创建 LLM Provider 实例
        llmProvider := e.resolveLLMProvider(agentCfg, userModelCfg)

        // 5. 构建 Chat 调用的模型参数选项
        chatOpts := e.buildChatOptions(agentCfg)

        // 6. 保存用户消息，获取历史
        e.mem.AddMessage(ctx, sessionID, memory.Message{Role: "user", Content: userMessage}, userID)
        history, _ := e.mem.History(ctx, sessionID)
        messages := buildMessages(prompt, history, e.maxContextMessages)

        // 7. 确定是否并行执行工具
        parallelToolCalls := e.parallelToolCalls
        if agentCfg != nil && agentCfg.ParallelToolCalls != nil {
            parallelToolCalls = *agentCfg.ParallelToolCalls
        }

        // 8. ReAct 循环
        for iter := 0; iter < maxIter; iter++ {
            ch <- StreamEvent{Type: "thinking", Iteration: iter + 1}
            e.callback.OnLLMStart(ctx, sessionID)

            // 8a. 非流式调用，获取完整决策
            result, err := llmProvider.Chat(ctx, messages, filteredTools, chatOpts...)
            if err != nil {
                ch <- StreamEvent{Type: "error", Error: err}
                return
            }

            // 8b. 无工具调用 → 直接将结果按 chunk 发送（无需再次调用 LLM）
            if len(result.ToolCalls) == 0 {
                e.emitContentAsChunks(ch, result.Content)
                e.mem.AddMessage(ctx, sessionID, memory.Message{
                    Role: "assistant", Content: result.Content,
                }, userID)
                e.callback.OnLLMEnd(ctx, sessionID, 0, 0)
                ch <- StreamEvent{Type: "done", Content: sessionID}
                return
            }

            // 8c. 有工具调用 → 执行并追加结果
            toolCallMsg := e.buildToolCallMessage(result.ToolCalls)
            e.mem.AddMessage(ctx, sessionID, toolCallMsg, userID)
            messages = append(messages, toolCallToLLMMessage(result))

            if parallelToolCalls && len(result.ToolCalls) > 1 {
                // 并行执行多个工具
                toolMsgs := e.executeToolsConcurrently(ctx, ch, result.ToolCalls, sessionID, userID)
                messages = append(messages, toolMsgs...)
            } else {
                // 串行执行工具
                for _, tc := range result.ToolCalls {
                    toolMsg := e.executeAndEmitTool(ctx, ch, tc, sessionID, userID)
                    messages = append(messages, toolMsg)
                }
            }
            e.callback.OnLLMEnd(ctx, sessionID, 0, 0)
        }

        // 超过 maxIter 仍未结束 → 强制以不带工具的 Chat 生成最终回答
        ch <- StreamEvent{Type: "thinking", Content: "max iterations reached, generating final answer"}
        result, err := llmProvider.Chat(ctx, messages, nil, chatOpts...)
        if err != nil {
            ch <- StreamEvent{Type: "error", Error: err}
            return
        }
        e.emitContentAsChunks(ch, result.Content)
        e.mem.AddMessage(ctx, sessionID, memory.Message{
            Role: "assistant", Content: result.Content,
        }, userID)
        e.callback.OnLLMEnd(ctx, sessionID, 0, 0)
        ch <- StreamEvent{Type: "done", Content: sessionID}
    }()

    return ch
}

// emitContentAsChunks 将完整文本按固定大小拆分为 chunk 事件发送，模拟流式效果
func (e *Engine) emitContentAsChunks(ch chan<- StreamEvent, content string) {
    const chunkSize = 4 // 每个 chunk 的字符数
    runes := []rune(content)
    for i := 0; i < len(runes); i += chunkSize {
        end := i + chunkSize
        if end > len(runes) {
            end = len(runes)
        }
        ch <- StreamEvent{Type: "chunk", Content: string(runes[i:end])}
    }
}

// executeAndEmitTool 执行单个工具并发送 SSE 事件，返回 LLM 消息
func (e *Engine) executeAndEmitTool(
    ctx context.Context, ch chan<- StreamEvent,
    tc llms.ToolCall, sessionID string, userID int64,
) llm.MessageContent {
    name := tc.FunctionCall.Name
    args := tc.FunctionCall.Arguments

    ch <- StreamEvent{
        Type: "tool_start", ToolCallID: tc.ID,
        ToolName: name, Arguments: args,
    }

    output, toolErr := e.executeToolWithTimeout(ctx, name, args)
    success := toolErr == nil
    resultStr := output
    if !success {
        resultStr = fmt.Sprintf("Error: %v", toolErr)
    }

    ch <- StreamEvent{
        Type: "tool_end", ToolCallID: tc.ID,
        ToolName: name, Result: resultStr, Success: success,
    }

    e.mem.AddMessage(ctx, sessionID, memory.Message{
        Role:    "tool",
        Content: buildToolResultJSON(tc.ID, name, resultStr),
    }, userID)

    return llm.MessageContent{
        Role: llm.RoleTool, Content: resultStr,
        ToolCallID: tc.ID, ToolCallName: name,
    }
}
```

#### 4.1.4 工具解析

```go
// resolveTools 根据 Agent 配置过滤可用工具
func (e *Engine) resolveTools(agentCfg *AgentConfig) []tools.Tool {
    if agentCfg == nil || len(agentCfg.Tools) == 0 {
        // 未配置工具 → 返回空列表（纯对话模式）
        return nil
    }
    return e.registry.GetByNames(agentCfg.Tools)
}
```

#### 4.1.5 模型配置合并

优先级：Agent 配置 > 用户全局配置 > 系统默认配置

```go
// resolveLLMProvider 合并多级模型配置并创建 Provider
func (e *Engine) resolveLLMProvider(agentCfg *AgentConfig, userCfg *models.ModelConfig) *llm.Provider {
    model, baseURL, token := "", "", ""

    // 用户全局配置
    if userCfg != nil && userCfg.Model != "" {
        model = userCfg.Model
        baseURL = userCfg.BaseURL
        token = userCfg.Token
    }

    // Agent 配置覆盖（更高优先级）
    if agentCfg != nil && agentCfg.ModelConfig != nil {
        if agentCfg.ModelConfig.Model != "" {
            model = agentCfg.ModelConfig.Model
        }
        // temperature/max_tokens/top_p 在 Chat 调用时通过 CallOption 覆盖
    }

    if model == "" {
        return e.llm // 无覆盖，使用系统默认
    }

    if p, err := e.llm.WithOverride(model, baseURL, token); err == nil {
        return p
    }
    return e.llm
}
```

#### 4.1.6 工具执行超时与安全

```go
// executeToolWithTimeout 带超时、panic 保护和 trace 追踪地执行工具
func (e *Engine) executeToolWithTimeout(ctx context.Context, name, input string) (output string, err error) {
    timeout := e.toolTimeout
    if timeout == 0 {
        timeout = 30 * time.Second
    }
    maxOutputLen := e.maxOutputLength
    if maxOutputLen == 0 {
        maxOutputLen = 4096
    }

    toolCtx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    // panic 保护
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("tool %q panicked: %v", name, r)
        }
    }()

    // 从 ctx 中提取 trace_id，传递给 callback 以贯穿调用链（PRD 5.3）
    traceID, _ := ctx.Value(middleware.TraceIDKey).(string)
    span := e.callback.OnToolStart(ctx, traceID, name, input)
    output, err = e.registry.Call(toolCtx, name, input)
    e.callback.OnToolEnd(span, name, output)

    // 输出截断，明确告知 LLM 数据不完整
    if len(output) > maxOutputLen {
        output = output[:maxOutputLen] + "\n...(output truncated at " + strconv.Itoa(maxOutputLen) + " chars, data may be incomplete)"
    }

    return
}
```

#### 4.1.7 构建 Chat 调用选项

```go
// buildChatOptions 根据 Agent 配置构建 Chat 方法的模型参数选项
func (e *Engine) buildChatOptions(agentCfg *AgentConfig) []llm.ChatOption {
    if agentCfg == nil || agentCfg.ModelConfig == nil {
        return nil
    }
    var opts []llm.ChatOption
    if agentCfg.ModelConfig.Temperature != nil {
        opts = append(opts, llm.WithTemperature(*agentCfg.ModelConfig.Temperature))
    }
    if agentCfg.ModelConfig.MaxTokens > 0 {
        opts = append(opts, llm.WithChatMaxTokens(agentCfg.ModelConfig.MaxTokens))
    }
    if agentCfg.ModelConfig.TopP != nil {
        opts = append(opts, llm.WithTopP(*agentCfg.ModelConfig.TopP))
    }
    return opts
}
```

### 4.2 LLM Provider 改造

#### 4.2.1 工具 Schema 传递增强

当前 `toLLMTools` 只传递了工具的 `Name` 和 `Description`，缺少 `Parameters`（JSON Schema）。LLM 需要参数 Schema 才能生成正确的 function call arguments。

```go
// internal/llm/provider.go

// ToolWithSchema 扩展 langchaingo tools.Tool 接口，支持参数 Schema
type ToolWithSchema interface {
    tools.Tool
    Parameters() map[string]any // 返回 JSON Schema
}

func toLLMTools(toolList []tools.Tool) []llms.Tool {
    result := make([]llms.Tool, len(toolList))
    for i, t := range toolList {
        fd := &llms.FunctionDefinition{
            Name:        t.Name(),
            Description: t.Description(),
        }
        // 如果工具实现了 ToolWithSchema，携带参数 Schema
        if ts, ok := t.(ToolWithSchema); ok {
            fd.Parameters = ts.Parameters()
        }
        result[i] = llms.Tool{
            Type:     "function",
            Function: fd,
        }
    }
    return result
}
```

#### 4.2.2 Chat 方法支持 Temperature/MaxTokens/TopP 覆盖

```go
// ChatOption 允许调用方覆盖模型参数
type ChatOption func(*chatOptions)

type chatOptions struct {
    temperature *float64
    maxTokens   *int
    topP        *float64
}

func WithTemperature(t float64) ChatOption {
    return func(o *chatOptions) { o.temperature = &t }
}

func WithChatMaxTokens(n int) ChatOption {
    return func(o *chatOptions) { o.maxTokens = &n }
}

func WithTopP(p float64) ChatOption {
    return func(o *chatOptions) { o.topP = &p }
}

func (p *Provider) Chat(ctx context.Context, messages []MessageContent, toolList []tools.Tool, opts ...ChatOption) (*ChatResult, error) {
    co := &chatOptions{}
    for _, o := range opts {
        o(co)
    }

    temp := p.cfg.Temperature
    if co.temperature != nil {
        temp = *co.temperature
    }
    maxTk := p.cfg.MaxTokens
    if co.maxTokens != nil {
        maxTk = *co.maxTokens
    }

    callOpts := []llms.CallOption{
        llms.WithTemperature(temp),
        llms.WithMaxTokens(maxTk),
    }
    if co.topP != nil {
        callOpts = append(callOpts, llms.WithTopP(*co.topP))
    }
    if len(toolList) > 0 {
        callOpts = append(callOpts, llms.WithTools(toLLMTools(toolList)))
    }

    // ... 其余逻辑不变
}
```

### 4.3 工具系统改造

#### 4.3.1 Registry 新增方法

```go
// internal/tool/registry.go

// GetByNames 按名称列表返回工具子集，忽略不存在的名称
func (r *Registry) GetByNames(names []string) []tools.Tool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    result := make([]tools.Tool, 0, len(names))
    for _, name := range names {
        if t, ok := r.tools[name]; ok {
            result = append(result, t)
        }
    }
    return result
}

// GetSchema 返回单个工具的 Schema 信息
func (r *Registry) GetSchema(name string) (ToolSchema, bool) {
    t, ok := r.Get(name)
    if !ok {
        return ToolSchema{}, false
    }
    schema := ToolSchema{
        Name:        t.Name(),
        Description: t.Description(),
    }
    if ts, ok := t.(llm.ToolWithSchema); ok {
        schema.Parameters = ts.Parameters()
    }
    return schema, true
}

// ToolSchema 工具元数据（供 API 返回）
type ToolSchema struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    Parameters  map[string]any `json:"parameters,omitempty"`
}
```

#### 4.3.2 内置工具补充 Parameters()

以 Calculator 为例：

```go
// internal/tool/calculator.go

func (c *Calculator) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "expression": map[string]any{
                "type":        "string",
                "description": "数学表达式，支持 +, -, *, /, %, ^(幂), sqrt(), abs()。示例: \"2 + 3 * 4\"",
            },
        },
        "required": []string{"expression"},
    }
}
```

DateTime：

```go
// internal/tool/datetime.go

func (d *DateTime) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "query": map[string]any{
                "type":        "string",
                "description": "查询类型：\"now\" 返回当前时间，\"date\" 返回日期，\"weekday\" 返回星期几，\"timestamp\" 返回 Unix 时间戳",
                "enum":        []string{"now", "date", "weekday", "timestamp"},
            },
        },
        "required": []string{"query"},
    }
}
```

### 4.4 Memory 系统改造

#### 4.4.1 Message 结构（保持不变）

```go
// internal/memory/interface.go

type Message struct {
    Role    string `json:"role"`    // "user" | "assistant" | "tool" | "assistant_tool_call"
    Content string `json:"content"` // 纯文本或 JSON（role=tool/assistant_tool_call 时为 JSON）
}
```

对于 role=tool 和 role=assistant_tool_call，工具调用元信息（tool_call_id、name 等）统一存储在 content JSON 中，不在 Message 结构体中冗余字段。这样 content JSON 是唯一权威数据源：

- role=4 的 content: `{"tool_calls": [{"id":"...", "name":"...", "arguments":"..."}]}`
- role=3 的 content: `{"tool_call_id":"...", "name":"...", "result":"..."}`

辅助函数用于构建 content JSON：

```go
// buildToolCallJSON 构建 role=4 的 content JSON
func buildToolCallJSON(toolCalls []llms.ToolCall) string {
    type tc struct {
        ID        string `json:"id"`
        Name      string `json:"name"`
        Arguments string `json:"arguments"`
    }
    var calls []tc
    for _, t := range toolCalls {
        calls = append(calls, tc{ID: t.ID, Name: t.FunctionCall.Name, Arguments: t.FunctionCall.Arguments})
    }
    b, _ := json.Marshal(map[string]any{"tool_calls": calls})
    return string(b)
}

// buildToolResultJSON 构建 role=3 的 content JSON
func buildToolResultJSON(toolCallID, name, result string) string {
    b, _ := json.Marshal(map[string]any{
        "tool_call_id": toolCallID,
        "name":         name,
        "result":       result,
    })
    return string(b)
}
```

#### 4.4.2 MySQLStore 适配

AddMessage 需要将新 role 映射到 tinyint：

```go
// internal/memory/mysql.go

func roleStringToInt(role string) int8 {
    switch role {
    case "user":
        return models.RoleUser           // 1
    case "assistant":
        return models.RoleAssistant      // 2
    case "tool":
        return models.RoleTool           // 3
    case "assistant_tool_call":
        return models.RoleAssistantToolCall // 4
    default:
        return models.RoleUser
    }
}

func roleIntToString(role int8) string {
    switch role {
    case models.RoleUser:
        return "user"
    case models.RoleAssistant:
        return "assistant"
    case models.RoleTool:
        return "tool"
    case models.RoleAssistantToolCall:
        return "assistant_tool_call"
    default:
        return "user"
    }
}
```

History 方法在 role=3/4 时需要还原 ToolCallID 和 ToolName（从 content JSON 中提取或新增数据库列）。

**方案选择**：在 content 字段中以 JSON 存储完整工具信息（无需新增列），History 查询后反序列化：

- role=4 的 content: `{"tool_calls": [{"id":"...", "name":"...", "arguments":"..."}]}`
- role=3 的 content: `{"tool_call_id":"...", "name":"...", "result":"..."}`

这样历史回放时前端可直接使用 content JSON 渲染工具调用块。

#### 4.4.3 上下文窗口管理

在 `buildMessages` 中实现截断策略：

```go
func buildMessages(systemPrompt string, history []Message, maxContextMessages int) []llm.MessageContent {
    msgs := []llm.MessageContent{{Role: llm.RoleSystem, Content: systemPrompt}}

    // 如果历史过长，从头部截断但保持 tool_call/tool 配对完整
    trimmed := trimHistory(history, maxContextMessages)

    for _, m := range trimmed {
        msgs = append(msgs, messageToLLM(m))
    }
    return msgs
}

// trimHistory 从尾部保留 maxN 条消息，确保 tool_call/tool 配对完整
func trimHistory(history []Message, maxN int) []Message {
    if len(history) <= maxN {
        return history
    }
    // 从 len-maxN 位置开始，向前查找到完整的 tool_call 开始位置
    start := len(history) - maxN
    for start > 0 && (history[start].Role == "tool" || history[start].Role == "assistant_tool_call") {
        start--
    }
    return history[start:]
}
```

### 4.5 StreamHandler 改造

#### 4.5.1 处理新事件类型

```go
// internal/handler/stream.go

// sseData 用于构建 SSE data JSON，避免 fmt.Sprintf 拼接导致的 JSON 注入问题
type sseChunkData struct {
    Content string `json:"content"`
}
type sseThinkingData struct {
    Iteration int `json:"iteration"`
}
type sseToolStartData struct {
    ToolCallID string          `json:"tool_call_id"`
    Name       string          `json:"name"`
    Arguments  json.RawMessage `json:"arguments"`
}
type sseToolEndData struct {
    ToolCallID string          `json:"tool_call_id"`
    Name       string          `json:"name"`
    Result     json.RawMessage `json:"result"`
    Success    bool            `json:"success"`
}
type sseSessionData struct {
    SessionID string `json:"session_id"`
}
type sseErrorData struct {
    Message string `json:"message"`
}

func (h *StreamHandler) Handle(c *gin.Context) {
    // ... 请求解析、SSE header 设置（不变）

    // 获取 Agent 完整配置
    agentCfg, err := h.resolveAgentConfig(req.AgentID)
    if err != nil {
        writeSSEJSON(c.Writer, flusher, "error", sseErrorData{Message: err.Error()})
        return
    }

    // 发送 session 事件（PRD 4.2.1 要求）
    writeSSEJSON(c.Writer, flusher, "session", sseSessionData{SessionID: sessionID})

    // 合并模型配置
    var userModelCfg *models.ModelConfig
    if h.modelConfigStore != nil {
        userModelCfg, _ = h.modelConfigStore.GetByUserAndType(userID, models.ModelTypeChat)
    }

    // 调用 Engine
    ch := h.engine.ProcessStream(ctx, sessionID, req.Message, agentCfg, userModelCfg, userID)

    for evt := range ch {
        switch evt.Type {
        case "chunk":
            writeSSEJSON(c.Writer, flusher, "chunk", sseChunkData{Content: evt.Content})

        case "thinking":
            writeSSEJSON(c.Writer, flusher, "thinking", sseThinkingData{Iteration: evt.Iteration})

        case "tool_start":
            writeSSEJSON(c.Writer, flusher, "tool_start", sseToolStartData{
                ToolCallID: evt.ToolCallID,
                Name:       evt.ToolName,
                Arguments:  ensureJSON(evt.Arguments),
            })

        case "tool_end":
            writeSSEJSON(c.Writer, flusher, "tool_end", sseToolEndData{
                ToolCallID: evt.ToolCallID,
                Name:       evt.ToolName,
                Result:     ensureJSON(evt.Result),
                Success:    evt.Success,
            })

        case "done":
            writeSSEJSON(c.Writer, flusher, "done", sseSessionData{SessionID: evt.Content})
            return

        case "error":
            writeSSEJSON(c.Writer, flusher, "error", sseErrorData{Message: evt.Error.Error()})
            return
        }
    }
}

// writeSSEJSON 使用 json.Marshal 序列化 data，确保输出合法 JSON
func writeSSEJSON(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
    b, _ := json.Marshal(data)
    fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
    flusher.Flush()
}

// ensureJSON 确保字符串是合法 JSON，若不是则作为 JSON 字符串处理
func ensureJSON(s string) json.RawMessage {
    if json.Valid([]byte(s)) {
        return json.RawMessage(s)
    }
    b, _ := json.Marshal(s)
    return json.RawMessage(b)
}

// resolveAgentConfig 从 AgentStore 获取完整的 Agent 配置
// 检查 Agent 状态，禁用的 Agent 返回错误
func (h *StreamHandler) resolveAgentConfig(agentID string) (*agent.AgentConfig, error) {
    if agentID == "" || h.agentStore == nil {
        return nil, nil
    }
    a, err := h.agentStore.Get(agentID)
    if err != nil {
        return nil, nil
    }
    if a.Status == models.AgentStatusDisabled {
        return nil, fmt.Errorf("agent %q is disabled", agentID)
    }
    cfg := &agent.AgentConfig{
        Prompt:        a.Prompt,
        Tools:         []string(a.Tools),
        MaxIterations: int(a.MaxIterations),
    }
    if a.ModelConfig != nil {
        cfg.ModelConfig = a.ModelConfig
    }
    return cfg, nil
}
```

### 4.6 AgentStore 改造

```go
// internal/agent/store.go

// Create 新增支持 tools、model_config、max_iterations
func (s *AgentStore) Create(userID int64, title, description, prompt string,
    tools []string, modelConfig *models.AgentModelConfig, maxIterations int8,
) (*models.Agent, error) {
    a := models.Agent{
        UUID:          genUUID(),
        UserID:        userID,
        Title:         title,
        Description:   description,
        Prompt:        prompt,
        Tools:         models.JSONStringSlice(tools),
        ModelConfig:   modelConfig,
        MaxIterations: maxIterations,
        Status:        1,
    }
    if a.MaxIterations == 0 {
        a.MaxIterations = 5
    }
    if a.MaxIterations > 10 {
        a.MaxIterations = 10 // PRD 要求上限为 10
    }
    if err := s.db.Create(&a).Error; err != nil {
        return nil, fmt.Errorf("insert agent: %w", err)
    }
    return &a, nil
}

// Update 新增支持 tools、model_config、max_iterations
func (s *AgentStore) Update(id string, updates map[string]interface{}) (*models.Agent, error) {
    // 字段白名单过滤（"description" 映射到 DB 列 "intro"）
    allowed := map[string]bool{
        "title": true, "intro": true, "description": true, "prompt": true,
        "tools": true, "model_config": true, "max_iterations": true, "status": true,
    }
    filtered := make(map[string]interface{})
    for k, v := range updates {
        if allowed[k] {
            // "description" 是 JSON 字段名，对应 DB 列 "intro"
            if k == "description" {
                filtered["intro"] = v
            } else {
                filtered[k] = v
            }
        }
    }
    if len(filtered) == 0 {
        return s.Get(id)
    }
    if err := s.db.Model(&models.Agent{}).Where("uuid = ?", id).Updates(filtered).Error; err != nil {
        return nil, fmt.Errorf("update agent: %w", err)
    }
    return s.Get(id)
}
```

### 4.7 配置系统扩展

```yaml
# configs/config.yaml 新增段

agent:
  max_iterations: 5            # 全局默认最大推理迭代次数
  tool_timeout: 30s            # 单个工具执行超时
  max_output_length: 4096      # 工具输出最大字符数
  parallel_tool_calls: true    # 是否允许并行工具调用
  max_context_messages: 50     # 历史消息最大保留条数（截断时保持 tool_call/tool 配对完整）
```

```go
// internal/config/config.go

type AgentConfig struct {
    MaxIterations      int           `mapstructure:"max_iterations"`
    ToolTimeout        time.Duration `mapstructure:"tool_timeout"`
    MaxOutputLength    int           `mapstructure:"max_output_length"`
    ParallelToolCalls  bool          `mapstructure:"parallel_tool_calls"`
    MaxContextMessages int           `mapstructure:"max_context_messages"`
}

type Config struct {
    // ... 现有字段
    Agent  AgentConfig  `mapstructure:"agent"`
}
```

### 4.8 新增 API 接口

#### GET /api/v1/tools/:name/schema

```go
// internal/handler/tool.go

func (h *ToolHandler) Schema(c *gin.Context) {
    name := c.Param("name")
    schema, ok := h.registry.GetSchema(name)
    if !ok {
        response.Fail(c, http.StatusNotFound, response.CodeNotFound, "tool not found")
        return
    }
    response.OK(c, schema)
}
```

---

## 5. 消息构建与 LLM 交互协议

### 5.1 消息列表结构（发送给 LLM）

每轮推理发送给 LLM 的完整消息列表：

```
[
  { role: "system",    content: "系统提示词..." },
  { role: "user",      content: "用户问题" },
  { role: "assistant", content: "", tool_calls: [{id:"call_1", name:"calculator", arguments:"..."}] },
  { role: "tool",      content: "1024", tool_call_id: "call_1", name: "calculator" },
  { role: "assistant", content: "", tool_calls: [{id:"call_2", name:"calculator", arguments:"..."}] },
  { role: "tool",      content: "3", tool_call_id: "call_2", name: "calculator" },
]
```

### 5.2 LangChainGo 适配

当前 `llm.MessageContent` 已有 `ToolCalls`、`ToolCallID`、`ToolCallName` 字段。`toLLMMessages` 也已处理 ToolCall 和 ToolCallResponse。无需修改，但 `buildMessages`（在 engine.go 中）需要支持新的 role 类型。

更新 `buildMessages`（签名与 Section 4.4.3 一致，接收 maxContextMessages 参数）：

```go
func buildMessages(systemPrompt string, history []memory.Message, maxContextMessages int) []llm.MessageContent {
    msgs := []llm.MessageContent{{Role: llm.RoleSystem, Content: systemPrompt}}

    trimmed := trimHistory(history, maxContextMessages)

    for _, m := range trimmed {
        switch m.Role {
        case "user":
            msgs = append(msgs, llm.MessageContent{Role: llm.RoleUser, Content: m.Content})
        case "assistant":
            msgs = append(msgs, llm.MessageContent{Role: llm.RoleAssistant, Content: m.Content})
        case "assistant_tool_call":
            // 反序列化 content JSON，提取 ToolCalls
            var tc toolCallContent
            json.Unmarshal([]byte(m.Content), &tc)
            msg := llm.MessageContent{Role: llm.RoleAssistant}
            for _, call := range tc.ToolCalls {
                msg.ToolCalls = append(msg.ToolCalls, llms.ToolCall{
                    ID: call.ID,
                    FunctionCall: llms.FunctionCall{Name: call.Name, Arguments: call.Arguments},
                })
            }
            msgs = append(msgs, msg)
        case "tool":
            // 反序列化 content JSON，提取 tool result
            var tr toolResultContent
            json.Unmarshal([]byte(m.Content), &tr)
            msgs = append(msgs, llm.MessageContent{
                Role: llm.RoleTool, Content: tr.Result,
                ToolCallID: tr.ToolCallID, ToolCallName: tr.Name,
            })
        }
    }
    return msgs
}
```

---

## 6. 并行工具调用

当 LLM 返回多个 ToolCalls 时，使用 `errgroup` 并发执行：

```go
const maxParallelTools = 5 // PRD 要求并行上限为 5

func (e *Engine) executeToolsConcurrently(
    ctx context.Context, ch chan<- StreamEvent,
    toolCalls []llms.ToolCall, sessionID string, userID int64,
) []llm.MessageContent {
    type toolResult struct {
        Index    int
        CallID   string
        Name     string
        Output   string
        Success  bool
        Message  llm.MessageContent
    }

    results := make([]toolResult, len(toolCalls))
    var mu sync.Mutex
    g, gCtx := errgroup.WithContext(ctx)
    g.SetLimit(maxParallelTools) // 限制并发数

    for i, tc := range toolCalls {
        i, tc := i, tc // capture
        name := tc.FunctionCall.Name
        args := tc.FunctionCall.Arguments

        // 发送 tool_start（串行，保证事件顺序）
        ch <- StreamEvent{
            Type: "tool_start", ToolCallID: tc.ID,
            ToolName: name, Arguments: args,
        }

        g.Go(func() error {
            output, err := e.executeToolWithTimeout(gCtx, name, args)
            success := err == nil
            resultStr := output
            if !success {
                resultStr = fmt.Sprintf("Error: %v", err)
            }

            mu.Lock()
            results[i] = toolResult{
                Index: i, CallID: tc.ID, Name: name,
                Output: resultStr, Success: success,
                Message: llm.MessageContent{
                    Role: llm.RoleTool, Content: resultStr,
                    ToolCallID: tc.ID, ToolCallName: name,
                },
            }
            mu.Unlock()
            return nil // 不中断其他工具
        })
    }
    g.Wait()

    // 按原始顺序发送 tool_end 并收集消息
    var msgs []llm.MessageContent
    for _, r := range results {
        ch <- StreamEvent{
            Type: "tool_end", ToolCallID: r.CallID,
            ToolName: r.Name, Result: r.Output, Success: r.Success,
        }
        msgs = append(msgs, r.Message)

        // 持久化
        e.mem.AddMessage(ctx, sessionID, memory.Message{
            Role:    "tool",
            Content: buildToolResultJSON(r.CallID, r.Name, r.Output),
        }, userID)
    }
    return msgs
}
```

---

## 7. 错误处理与边界条件

### 7.1 错误处理策略

| 场景 | 处理方式 |
|------|---------|
| LLM 调用失败（网络/超时） | 发送 `error` 事件，终止循环 |
| 工具未找到（名称无效） | 返回错误文本作为 tool result，让 LLM 自行处理 |
| 工具执行超时 | 返回 "Error: tool execution timeout" 作为 result |
| 工具 panic | recover 捕获，返回 "Error: tool panicked" 作为 result |
| 工具输出过长 | 截断到 maxOutputLength 并追加明确的不完整数据提示 |
| 超过 maxIter | 强制调用 Chat（不带工具）生成最终回答，将结果按 chunk 发送 |
| SSE 连接断开 | ctx.Done() 触发，清理资源 |
| Agent 已禁用 | resolveAgentConfig 返回错误，发送 error 事件 |

### 7.2 工具失败不中断循环

单个工具失败时，错误信息作为 tool result 返回给 LLM，让 LLM 决定：
- 尝试用另一个工具
- 用已有信息生成回答
- 告知用户工具不可用

这保证了系统的鲁棒性（对应 US-6）。

### 7.3 请求取消

用户断开 SSE 连接时，gin 的 `c.Request.Context()` 被 cancel，传播到：
- Engine 的 `ctx.Done()` → 终止循环
- 工具的 `toolCtx` → 中断工具执行
- LLM 的 `ctx` → 中断 LLM 调用

---

## 8. 数据库迁移

### 8.1 迁移 SQL

```sql
-- Migration: 001_agent_tools_support.sql

-- 1. Agent 表新增字段
ALTER TABLE ai_agent
    ADD COLUMN tools JSON DEFAULT NULL COMMENT '启用的工具名称列表' AFTER prompt,
    ADD COLUMN model_config JSON DEFAULT NULL COMMENT '模型参数覆盖' AFTER tools,
    ADD COLUMN max_iterations TINYINT NOT NULL DEFAULT 5 COMMENT '最大推理迭代次数' AFTER model_config,
    ADD COLUMN status TINYINT NOT NULL DEFAULT 1 COMMENT '状态: 1=启用 2=禁用' AFTER max_iterations;

-- 2.（可选）更新默认 Agent 的工具列表
-- 注意：此操作会给所有存量 Agent 添加默认工具，请根据业务需求决定是否执行
-- 如果部分 Agent 不需要工具能力（纯对话模式），不应执行此语句
-- UPDATE ai_agent
-- SET tools = '["calculator","datetime"]'
-- WHERE tools IS NULL;
```

### 8.2 回滚 SQL

```sql
ALTER TABLE ai_agent
    DROP COLUMN tools,
    DROP COLUMN model_config,
    DROP COLUMN max_iterations,
    DROP COLUMN status;
```

### 8.3 兼容性

- 新字段均有默认值，存量数据无需迁移
- `tools = NULL` 表示未配置工具，Engine 按"纯对话模式"处理
- `max_iterations = 5` 与当前硬编码值一致
- `status = 1` 表示启用，不影响存量数据

---

## 9. 启动流程变更

```go
// cmd/server/main.go

// 变更点：Engine 构造时传入 AgentConfig
agentCfg := &config.AgentConfig{
    MaxIterations:      cfg.Agent.MaxIterations,
    ToolTimeout:        cfg.Agent.ToolTimeout,
    MaxOutputLength:    cfg.Agent.MaxOutputLength,
    ParallelToolCalls:  cfg.Agent.ParallelToolCalls,
    MaxContextMessages: cfg.Agent.MaxContextMessages,
}
engine := agent.New(llmProvider, reg, sessionStore, cb,
    agent.WithMaxIter(agentCfg.MaxIterations),
    agent.WithToolTimeout(agentCfg.ToolTimeout),
    agent.WithMaxOutputLength(agentCfg.MaxOutputLength),
    agent.WithParallelToolCalls(agentCfg.ParallelToolCalls),
    agent.WithMaxContextMessages(agentCfg.MaxContextMessages),
)
```

其余启动步骤不变。

---

## 10. 测试方案

### 10.1 单元测试

| 测试用例 | 覆盖场景 |
|---------|---------|
| `TestProcessStream_NoTools` | Agent 未配置工具 → 纯对话模式（退化为现有行为） |
| `TestProcessStream_SingleToolCall` | LLM 返回 1 个 ToolCall → 执行 → 最终回答 |
| `TestProcessStream_MultiRound` | LLM 返回 ToolCall → 执行 → 再次 ToolCall → 执行 → 最终回答 |
| `TestProcessStream_ParallelTools` | LLM 返回 2 个 ToolCall → 并发执行 → 最终回答 |
| `TestProcessStream_SerialTools` | parallel_tool_calls=false 时串行执行工具 |
| `TestProcessStream_ToolTimeout` | 工具执行超时 → 错误作为 result → LLM 处理 |
| `TestProcessStream_ToolPanic` | 工具 panic → recover → 错误作为 result |
| `TestProcessStream_MaxIter` | 达到 maxIter → 强制生成最终回答 |
| `TestProcessStream_ToolNotFound` | ToolCall 中工具名不存在 → 错误 result |
| `TestProcessStream_ContextCancel` | 中途取消 → 发送 error 事件 |
| `TestResolveTools_FilterByNames` | 按名称过滤工具子集 |
| `TestResolveLLMProvider_Priority` | 模型配置合并优先级 |
| `TestBuildMessages_WithToolHistory` | 包含 tool_call/tool 的历史正确构建 |
| `TestTrimHistory_KeepPairs` | 截断时保持 tool_call/tool 配对完整 |
| `TestEmitContentAsChunks` | 文本按 4 字符拆分为 chunk 事件 |
| `TestBuildChatOptions` | 从 AgentConfig 构建 ChatOption（含 Temperature/MaxTokens/TopP） |

### 10.2 Mock 策略

- **LLM Provider**: mock 实现，可按序返回预设的 `ChatResult`（含/不含 ToolCalls）
- **Tool Registry**: 注册 mock 工具（可控制执行时间、返回值、是否 panic）
- **Memory Store**: 使用 InMemoryStore

### 10.3 集成测试

| 测试用例 | 验证点 |
|---------|--------|
| 数学计算端到端 | 发送计算问题 → 收到 session + thinking + tool_start + tool_end + chunk + done |
| 多轮工具调用 | 复杂计算 → 多个 tool_start/tool_end 对 → 最终答案正确 |
| Agent 配置生效 | 创建限定工具的 Agent → 对话时只调用配置的工具 |
| 历史回放 | 对话后查询历史 → 包含 role=3/4 消息 → 前端可渲染 |
| 禁用 Agent | status=2 的 Agent → 返回 error 事件，不执行任何工具 |

---

## 11. 实施顺序

按依赖关系排列：

```
Phase 1（第 1 周）：底层基础
├── 1.1 models/agent.go — 新增字段 + JSONStringSlice 类型（已完成 ✅）
├── 1.2 models/chat_message.go — 新增 role 常量（已完成 ✅）
├── 1.3 memory/interface.go — Message 结构不变（工具元信息存于 content JSON）
├── 1.4 memory/mysql.go — role 映射扩展
├── 1.5 数据库迁移执行
└── 1.6 config/config.go — 新增 AgentConfig（含 ParallelToolCalls、MaxContextMessages）

Phase 2（第 2 周）：核心循环
├── 2.1 tool/*.go — 各工具实现 Parameters()
├── 2.2 tool/registry.go — GetByNames + GetSchema
├── 2.3 llm/provider.go — toLLMTools 传递 Parameters + ChatOption（含 TopP）
├── 2.4 agent/engine.go — ReAct 循环 + 工具执行 + emitContentAsChunks
├── 2.5 agent/engine.go — 并行工具调用（errgroup + SetLimit(5)）
└── 2.6 单元测试全覆盖（16 项）

Phase 3（第 3 周）：API 层 + 串联
├── 3.1 agent/store.go — Create/Update 支持新字段（含 max_iterations 上限校验）
├── 3.2 handler/agent.go — CRUD 接口适配
├── 3.3 handler/stream.go — writeSSEJSON + JSON 类型结构体 + session 事件 + 状态检查
├── 3.4 handler/tool.go — Schema 接口
├── 3.5 cmd/server/main.go — 启动流程适配
└── 3.6 集成测试（5 项，含禁用 Agent 测试）

Phase 4（第 4 周）：前端 + 收尾
├── 4.1 前端对话界面工具调用渲染
├── 4.2 前端 Agent 编辑页工具选择 + 参数配置
├── 4.3 历史消息 tool 类型渲染
├── 4.4 Agent 模板系统（模型 + API + 种子数据）
└── 4.5 文档更新 + 上线
```

---

## 12. 风险与技术决策记录

### 12.1 关键技术决策

| 决策 | 选项 | 选择 | 理由 |
|------|------|------|------|
| LLM 调用模式 | A: 全流式 B: 非流式+最后流式 C: 全非流式+拆分 chunk | C | 流式回调无法获取完整 ToolCalls；B 方案最终回答需二次调用 LLM 浪费 token；C 方案复用 Chat 结果按 chunk 发送，兼顾体验与成本 |
| 工具消息存储 | A: content JSON B: 新增列 | A | 无需 DDL 变更，灵活支持各种工具元数据，content JSON 为唯一权威数据源 |
| 工具失败处理 | A: 中断循环 B: 错误作为 result | B | 让 LLM 自行判断如何利用失败信息，更鲁棒 |
| 上下文截断 | A: 固定条数 B: token 估算 | A | 实现简单，token 估算依赖 tokenizer（增加依赖），条数通过 max_context_messages 可配置 |
| 并行工具执行 | A: 串行 B: errgroup 并发 | B | 减少等待时间，符合 PRD 要求，通过 parallel_tool_calls 可配置 |
| SSE JSON 序列化 | A: fmt.Sprintf 拼接 B: json.Marshal | B | 避免 JSON 注入风险，保证所有 SSE data 为合法 JSON |

### 12.2 风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| LangChainGo v0.1.13 的 ToolCall 支持不完整 | 无法正确解析 ToolCalls | 升级 LangChainGo 或自行解析 response |
| LLM 生成的 tool arguments 格式不合法 | JSON 解析失败 | 在 tool.Call 入口做容错解析 |
| 大量并行工具调用占满连接池 | 其他请求超时 | 限制并行数 ≤ 5，使用独立 goroutine 池 |
| 工具 content JSON 格式后续难以变更 | 数据兼容问题 | 在 JSON 中加入 version 字段，Reader 做兼容 |
