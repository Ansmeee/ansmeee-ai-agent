# 通用 Agent 智能体 — 产品需求文档

## 1. 产品概述

### 1.1 背景

当前系统已具备基础的 AI 对话能力（LLM 流式输出、会话管理、Agent CRUD），但 Agent Engine 缺乏真正的"智能体"能力——即自主调用工具、多轮推理、根据工具结果决定下一步行动的能力。用户创建的 Agent 本质上只是一个"带 System Prompt 的对话"，无法完成需要多步骤推理和外部交互的复杂任务。

### 1.2 目标

构建一个通用 Agent 智能体框架，使用户能够：

1. **创建具备工具调用能力的智能体** — Agent 可以根据用户意图自主选择并执行工具
2. **多轮工具调用推理** — Agent 能执行 LLM → Tool → LLM → Tool 的迭代循环，直到任务完成
3. **灵活配置** — 用户可为不同 Agent 配置不同的工具集、模型参数、行为策略
4. **实时可观测** — 用户可以在对话过程中看到 Agent 的思考链路和工具调用过程

### 1.3 核心价值

| 维度 | 当前状态 | 目标状态 |
|------|---------|---------|
| 工具调用 | 工具已注册但未被执行 | Agent 自主选择并执行工具 |
| 推理能力 | 单轮 LLM 输出 | 多轮推理，最多 N 次迭代 |
| 可配置性 | 仅 System Prompt | Prompt + 工具集 + 模型参数 + 策略 |
| 可观测性 | 仅文本流 | 文本流 + 工具调用状态 + 思考过程 |

---

## 2. 用户角色

| 角色 | 描述 |
|------|------|
| 普通用户 | 使用 Agent 完成日常任务（问答、计算、信息查询） |
| 高级用户 | 创建和配置自定义 Agent，调整工具集和行为策略 |
| 管理员 | 管理系统工具、审计 Agent 行为 |

---

## 3. 用户故事

### 3.1 核心用户故事

**US-1: 作为用户，我希望 Agent 能自动调用工具获取信息来回答我的问题**

> 我问"今天是星期几？"，Agent 应该调用 DateTime 工具获取当前时间后再回答我，而不是猜测。

验收标准：
- Agent 识别需要工具辅助的问题
- 自动选择合适的工具并执行
- 基于工具返回结果生成最终回答
- 整个过程通过 SSE 流式传输给用户

**US-2: 作为用户，我希望能看到 Agent 使用工具的过程**

> 当 Agent 调用计算器工具时，我想在界面上看到"正在计算: 123 * 456"这样的中间状态。

验收标准：
- SSE 流中包含工具调用事件（工具名、输入参数）
- SSE 流中包含工具执行结果事件
- 前端实时展示工具调用过程
- 区分"思考中"、"调用工具中"、"生成回答中"三种状态

**US-3: 作为用户，我希望 Agent 能进行多步推理**

> 我问"计算 (2^10 + 365) / 7 的余数是多少？"，Agent 应该分步计算：先算 2^10，再加 365，最后算除以 7 的余数。

验收标准：
- Agent 支持多轮 LLM → Tool → LLM 循环
- 最大迭代次数可配置（默认 5 次）
- 超过最大迭代时优雅终止并告知用户
- 每一轮的工具调用和结果都可见

**US-4: 作为高级用户，我希望为不同 Agent 配置不同的工具集**

> 我创建了一个"数学助手"Agent，它只需要计算器工具；另一个"信息助理"Agent 需要搜索和天气工具。

验收标准：
- Agent 创建/编辑时可选择启用的工具
- Agent 对话时只使用其配置的工具
- 未配置工具的 Agent 退化为纯对话模式
- 工具列表从系统注册表获取

**US-5: 作为高级用户，我希望配置 Agent 的模型参数**

> 我希望创意写作 Agent 使用更高的 temperature，而代码 Agent 使用更低的 temperature。

验收标准：
- Agent 配置支持模型参数覆盖（temperature、max_tokens、top_p）
- 支持指定不同的模型名称
- 配置优先级：Agent 配置 > 用户全局配置 > 系统默认配置

### 3.2 扩展用户故事

**US-6: 作为用户，我希望 Agent 能处理工具调用失败的情况**

验收标准：
- 工具执行超时有合理处理（默认 30s）
- 工具返回错误时，Agent 尝试替代方案或告知用户
- 不会因为单个工具失败而中断整个对话

**US-7: 作为用户，我希望能中断 Agent 的执行**

验收标准：
- 用户可在 Agent 推理过程中发送取消信号
- Agent 优雅停止当前工具链并返回已有结果
- 取消后会话状态保持一致

**US-8: 作为高级用户，我希望使用 Agent 模板快速创建常用类型的 Agent**

验收标准：
- 系统提供预置 Agent 模板（通用助手、数学助手、信息查询等）
- 模板包含推荐的 Prompt、工具集和参数
- 用户可基于模板创建并自定义

---

## 4. 功能需求

### 4.1 Agent Engine — 工具调用循环（P0）

#### 4.1.1 ReAct 推理循环

Engine 实现 Reasoning + Acting 循环：

```
用户消息 → [System Prompt + History + Tools Schema]
    → LLM 推理
        → 如果返回 ToolCalls:
            → 逐个执行工具
            → 将工具结果追加到消息列表
            → 回到 LLM 推理（迭代 +1）
        → 如果返回纯文本:
            → 流式输出给用户
            → 结束循环
    → 超过 maxIter:
        → 强制生成最终回答
        → 结束循环
```

核心规则：
- 每轮迭代中，LLM 可返回多个并行 ToolCalls
- 并行 ToolCalls 并发执行，全部完成后进入下轮推理
- 工具执行结果作为 Tool Role 消息追加到上下文
- 最终文本响应以流式方式输出

#### 4.1.2 工具调用格式

遵循 OpenAI Function Calling 协议：

```json

{
  "id": "call_abc123",
  "type": "function",
  "function": {
    "name": "calculator",
    "arguments": "{\"expression\": \"2^10 + 365\"}"
  }
}
```
```json
{
  "role": "tool",
  "tool_call_id": "call_abc123",
  "content": "1389"
}
```

#### 4.1.3 配置项

| 参数 | 默认值 | 说明 |
|------|--------|------|
| max_iterations | 5 | 最大推理循环次数 |
| tool_timeout | 30s | 单个工具执行超时 |
| parallel_tool_calls | true | 是否允许并行工具调用 |

### 4.2 SSE 流式事件协议（P0）

#### 4.2.1 事件类型

在现有 `chunk`、`done`、`error` 基础上新增事件：

| 事件类型 | 触发时机 | 数据格式 |
|---------|---------|---------|
| `session` | 连接建立 | `{"session_id": "xxx"}` |
| `chunk` | LLM 文本流 | `{"content": "..."}` |
| `tool_start` | 开始调用工具 | `{"tool_call_id": "xxx", "name": "calculator", "arguments": "..."}` |
| `tool_end` | 工具返回结果 | `{"tool_call_id": "xxx", "name": "calculator", "result": "...", "success": true}` |
| `thinking` | Agent 开始新一轮推理 | `{"iteration": 2}` |
| `done` | 完成 | `{"session_id": "xxx"}` |
| `error` | 错误 | `{"message": "..."}` |

#### 4.2.2 事件流示例

```
event: session
data: {"session_id": "abc-123"}

event: thinking
data: {"iteration": 1}

event: tool_start
data: {"tool_call_id": "call_1", "name": "calculator", "arguments": "{\"expression\": \"2^10\"}"}

event: tool_end
data: {"tool_call_id": "call_1", "name": "calculator", "result": "1024", "success": true}

event: thinking
data: {"iteration": 2}

event: chunk
data: {"content": "2的10次方"}

event: chunk
data: {"content": "等于1024。"}

event: done
data: {"session_id": "abc-123"}
```

### 4.3 Agent 配置增强（P0）

#### 4.3.1 Agent 数据模型扩展

在现有 `ai_agent` 表基础上新增字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| tools | JSON | 启用的工具名称列表，如 `["calculator","datetime"]` |
| model_config | JSON | 模型参数覆盖 |
| max_iterations | int | 最大推理迭代次数（默认 5） |
| status | tinyint | 状态：1=启用 2=禁用 |

`model_config` JSON 结构：

```json
{
  "model": "deepseek-chat",
  "temperature": 0.7,
  "max_tokens": 4096,
  "top_p": 1.0
}
```

#### 4.3.2 Agent CRUD API 增强

更新 Agent 的创建和编辑接口，支持新字段：

**POST /api/v1/agents**（创建）

```json
{
  "title": "数学助手",
  "description": "擅长数学计算和推理",
  "prompt": "你是一个数学助手...",
  "tools": ["calculator"],
  "model_config": {
    "temperature": 0.3
  },
  "max_iterations": 3
}
```

**PUT /api/v1/agents/:id**（更新）— 同上结构，支持部分字段更新。

**GET /api/v1/agents/:id**（获取）— 返回完整配置含新字段。

### 4.4 工具系统增强（P1）

#### 4.4.1 工具 Schema 定义

每个工具需提供标准化的元数据，供 LLM Function Calling 使用：

- **名称** — 工具的唯一标识符
- **描述** — 工具的功能说明（供 LLM 理解何时调用）
- **参数 Schema** — JSON Schema 格式的参数描述，定义工具接受的输入字段、类型和必填项
- **执行能力** — 接收输入并返回文本结果

#### 4.4.2 内置工具完善

| 工具名 | 当前状态 | 目标状态 |
|--------|---------|---------|
| Calculator | 已实现 | 补充 JSON Schema |
| DateTime | 已实现 | 补充 JSON Schema |
| WebSearch | Stub | 接入搜索 API（SerpAPI / Tavily） |
| Weather | Stub | 接入天气 API（和风天气 / OpenWeather） |

#### 4.4.3 工具执行安全

- 工具执行带超时控制（默认 30s）
- 工具输出长度限制（默认 4096 字符，超出截断）
- 工具执行隔离，单个工具崩溃不影响主进程
- 工具调用日志记录（用于审计和调试）

### 4.5 前端交互（P1）

#### 4.5.1 对话界面 — 工具调用展示

在对话气泡中展示 Agent 的工具调用过程：

```
┌─────────────────────────────────────────┐
│ [用户] 帮我计算 2^10 + 365 除以 7 的余数  │
├─────────────────────────────────────────┤
│ [Agent]                                 │
│                                         │
│  ┌─ 🔧 调用工具: calculator ──────────┐ │
│  │ 输入: 2^10 + 365                   │ │
│  │ 结果: 1389                         │ │
│  └────────────────────────────────────┘ │
│                                         │
│  ┌─ 🔧 调用工具: calculator ──────────┐ │
│  │ 输入: 1389 % 7                     │ │
│  │ 结果: 3                            │ │
│  └────────────────────────────────────┘ │
│                                         │
│  2^10 + 365 = 1389，除以 7 的余数是 3。 │
└─────────────────────────────────────────┘
```

UI 要求：
- 工具调用块默认折叠，可展开查看详情
- 工具调用中显示 loading 动画
- 工具成功/失败有不同样式区分
- 思考过程（iteration）有视觉指示

#### 4.5.2 Agent 编辑界面 — 工具配置

在 Agent 编辑页面增加：
- 工具多选列表（显示工具名 + 描述）
- 模型参数配置表单（temperature 滑块、max_tokens 输入框）
- 最大迭代次数配置
- 预览/测试按钮（可在编辑时快速测试 Agent）

#### 4.5.3 工具列表页面

新增工具管理页面，展示：
- 所有可用工具的名称、描述
- 每个工具的参数 Schema（格式化展示）
- 工具的使用统计（调用次数、平均耗时）

### 4.6 记忆与上下文管理（P1）

#### 4.6.1 工具调用消息持久化

工具调用的完整消息链需要持久化，以支持历史回放：

消息角色扩展：

| role 值 | 含义 |
|---------|------|
| 1 | user（用户消息） |
| 2 | assistant（AI 回复） |
| 3 | tool（工具返回结果） |
| 4 | assistant_tool_call（AI 工具调用请求） |

`assistant_tool_call` 消息的 content 结构：

```json
{
  "tool_calls": [
    {
      "id": "call_abc",
      "name": "calculator",
      "arguments": "{\"expression\": \"2^10\"}"
    }
  ]
}
```

#### 4.6.2 上下文窗口管理

当历史消息超过模型上下文窗口时：
- 优先保留 System Prompt
- 保留最近 N 条完整的 LLM↔Tool 交互轮次
- 早期消息按时间顺序截断
- 截断时保持 ToolCall 和 Tool Result 的配对完整性

---

## 5. 非功能需求

### 5.1 性能

| 指标 | 要求 |
|------|------|
| 工具调用延迟 | 单个工具执行 < 30s（含网络） |
| 并行工具调用 | 支持同时执行 ≤ 5 个工具 |
| SSE 首字节 | < 2s（不含 LLM 首 token 时间） |
| 最大迭代 | 默认 5 次，可配置最高 10 次 |

### 5.2 可靠性

- 工具执行失败不应导致整个请求失败
- Agent 循环超时有兜底机制（总超时 = max_iterations * tool_timeout * 2）
- SSE 连接断开时正确清理资源
- 工具崩溃不影响主进程

### 5.3 可观测性

- 每次工具调用记录：工具名、输入、输出、耗时、是否成功
- Agent 推理链路可追踪（trace_id 贯穿整个调用链）
- 关键指标上报：迭代次数分布、工具调用成功率、平均推理时间

### 5.4 安全性

- 工具执行在沙箱环境中（无法访问系统资源）
- 工具输入参数校验（防注入）
- 工具调用频率限制（防止 Agent 循环调用）
- 敏感工具需要用户确认（预留机制）

---

## 6. API 变更汇总

### 6.1 变更接口

| 接口 | 变更内容 |
|------|---------|
| POST /api/v1/agents | Request Body 新增 `tools`, `model_config`, `max_iterations` |
| PUT /api/v1/agents/:id | 同上 |
| GET /api/v1/agents/:id | Response 新增上述字段 |
| GET /api/v1/agents | Response 列表项新增上述字段 |
| POST /api/v1/chat/completion | SSE 新增 `tool_start`, `tool_end`, `thinking` 事件 |
| GET /api/v1/chat/:sessionId | Response 消息包含 role=3(tool) 和 role=4(tool_call) |

### 6.2 新增接口

| 接口 | 说明 |
|------|------|
| GET /api/v1/tools/:name/schema | 获取工具的 JSON Schema（供前端展示） |

---

## 7. 实施计划

### Phase 1: Agent Engine 核心能力（P0）— 1 周

- [ ] 实现 ReAct 推理循环（工具调用迭代）
- [ ] 工具补充参数 Schema 描述
- [ ] SSE 流新增 `tool_start`、`tool_end`、`thinking` 事件
- [ ] 工具执行超时和错误处理
- [ ] 工具调用消息持久化（role=3, role=4）
- [ ] 测试覆盖：正常流、多轮迭代、超时、工具失败

### Phase 2: Agent 配置增强（P0）— 1 周

- [ ] Agent 数据模型新增字段（tools, model_config, max_iterations, status）
- [ ] Agent CRUD API 支持新字段
- [ ] Agent 推理时按配置过滤可用工具
- [ ] 模型参数优先级合并逻辑（Agent > 用户 > 系统默认）
- [ ] 前端 Agent 编辑页支持工具选择和参数配置

### Phase 3: 前端交互升级（P1）— 1 周

- [ ] 对话界面展示工具调用过程（折叠块）
- [ ] 工具调用 loading 动画和状态指示
- [ ] Agent 编辑页工具多选和模型参数表单
- [ ] 历史消息回放支持 tool 类型消息渲染

### Phase 4: 工具生态与可观测性（P1）— 1 周

- [ ] WebSearch 工具接入真实 API
- [ ] Weather 工具接入真实 API
- [ ] 工具调用追踪和日志完善
- [ ] 工具调用统计仪表盘（管理后台）
- [ ] Agent 模板系统

---

## 8. 验收标准

### 8.1 功能验收

1. 用户发送"今天是星期几"，Agent 自动调用 DateTime 工具并基于结果回答
2. 用户发送复杂计算问题，Agent 能进行多轮工具调用（≥ 2 轮）后给出最终答案
3. 创建 Agent 时可配置工具列表，对话时仅使用已配置的工具
4. SSE 流中正确包含所有事件类型，前端正确渲染
5. 工具执行超时时，Agent 能优雅处理并给出说明
6. 历史对话回放包含完整的工具调用链路

### 8.2 非功能验收

1. 单次工具调用链路（含 LLM 推理）总耗时 < 60s
2. 并发 10 个用户同时使用 Agent 时系统稳定
3. 工具崩溃不影响其他请求
4. 所有工具调用有完整的 trace 日志

---

## 9. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| LLM 不正确使用工具（幻觉参数） | Agent 输出错误结果 | 工具参数校验 + 清晰的 Schema 描述 |
| Agent 无限循环调用工具 | 资源耗尽、用户等待过长 | maxIter 硬限制 + 总超时兜底 |
| 工具调用增加 token 消耗 | 用户成本上升 | 工具结果截断 + 用户可配置 maxIter |
| 并行工具调用 race condition | 数据不一致 | 工具执行结果按 ID 匹配，无共享状态 |
| 第三方 API 不稳定（搜索、天气） | Agent 功能降级 | 重试机制 + 优雅降级提示 |

---

## 10. 未来演进方向（Out of Scope）

以下能力不在本期范围内，但架构设计应预留扩展点：

- **自定义工具注册** — 用户通过 API/UI 注册自定义 HTTP 工具
- **RAG 集成** — Agent 结合向量检索（Milvus）进行知识增强回答
- **多 Agent 协作** — 多个 Agent 之间传递任务和上下文
- **Agent 工作流** — 可视化编排 Agent 的多步骤执行流程
- **工具审批机制** — 敏感工具执行前需要用户确认
- **Agent 市场** — 用户共享和发布 Agent 配置
- **长期记忆** — 跨会话的用户偏好和知识积累
