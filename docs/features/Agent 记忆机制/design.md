# Agent 通用记忆机制 — 技术方案设计（design.md）

> 项目：ansmeee-ai-agent  
> 依据：`docs/features/Agent 记忆机制/proposal.md`（v3 三层架构）  
> 本文聚焦「怎么实现」：包结构、接口签名、数据模型、与现有代码的精确集成点、分期落地清单。

---

## 一、范围与对齐现状

proposal 定义了三层（L0 对话 / L1 任务状态机 / L2 长期记忆三通道）与写入/读取/演化三链路。本设计将其落到现有代码：

| 现有代码 | 事实 | 本方案关系 |
|---------|------|-----------|
| `internal/memory` · `Message{Role,Content}` / `SessionStore` | L0，已有 InMemory/Redis/MySQL 三实现 | **不改接口**，仅给 `Message` 加可选字段、`sessions` 加两列 |
| `internal/agent/engine.go` · `ProcessStream(ctx, sessionID, userMessage, *AgentConfig, *models.ModelConfig, userID) <-chan StreamEvent` | ReAct 主循环 + `buildMessages(systemPrompt, history)` | 注入点：**增强 systemPrompt 字符串**；循环末尾触发提取 |
| `internal/config/config.go` · `MemoryConfig{Type,TTL,MaxMessages}` + 顶层 `Milvus` | Viper 加载 | 扩展 `MemoryConfig` 嵌套子结构；向量通道复用顶层 `Milvus` |
| `cmd/server/main.go` · `agent.New(llmProvider, reg, sessionStore, cb, opts...)` | DI 装配 | 新增 store 构造 + `agent.WithMemoryManager(mm)` option |

**关键对齐修正**（proposal 伪代码与真实签名的差异，以本文为准）：
- `ProcessStream` 返回 `<-chan StreamEvent`，非 `<-chan string`；`agentCfg` 是 `*AgentConfig` 指针且带 `modelCfg *models.ModelConfig`。
- 增强注入不新建 message，而是**在 `buildMessages` 前把增强段拼进 `prompt` 字符串**（`buildMessages` 的首元素即 system message）。
- L1 注入用「上一轮载入的」TaskState（循环前注入即正确），当前轮更新在循环末尾回写供下一轮用——天然规避「注入早于填充」的时序问题。
- `AgentConfig` 无 ID 字段、`ProcessStream` 无 agentID 参数，故 **MVP 记忆一律用户级**（`agent_id=''`）；agent 级隔离需先扩签名，不在 MVP 范围（详见 §五 5.2）。

---

## 二、代码结构（全部落在 `internal/memory` 包，避免 import 环）

现有 `agent` 已 `import internal/memory`；新增类型同包即可，无循环依赖。

```
internal/memory/
  interface.go        (已有) Message / SessionStore —— Message 增 3 个可选字段
  memory.go / redis.go / mysql.go (已有) L0 三实现 —— mysql sessions 表加 2 列
  taskstate.go        (新) TaskState / Step / 规则状态机 updateTaskState()
  taskstore.go        (新) TaskStore 接口 + memTaskStore / redisTaskStore
  entry.go            (新) MemoryEntry / PolicyEntry / GORM 模型 + AutoMigrate
  fact_store.go       (新) FactStore 接口 + gormFactStore（含 admission + scoring）
  policy_store.go     (新) PolicyStore 接口 + gormPolicyStore
  vector_store.go     (新) VectorStore 接口 + milvusVectorStore（Phase 3，默认 nil）
  embedding.go        (新) EmbeddingProvider（Phase 3）
  extractor.go        (新) 确定性提取器 DeterministicExtractor（主路径，零 LLM）
  summarizer.go       (新) LLM 摘要提取器 LLMSummarizer（次路径，每会话≤1）
  router.go           (新) QueryRouter 确定性路由
  manager.go          (新) MemoryManager 编排：Retrieve / OnTurnEnd / OnIdle
  idle_scan.go        (新) 后端无关静默扫描器 IdleScanner
internal/agent/engine.go   (改) 注入增强 + 循环末尾触发 + WithMemoryManager option
internal/config/config.go  (改) MemoryConfig 扩展 + applyDefaults 默认值
cmd/server/main.go         (改) 构造 stores/manager，装配进 engine，启动 IdleScanner
```

---

## 三、数据层

### 3.1 L0 现有结构的最小改动

```go
// interface.go — 向后兼容，新增字段皆可选
type Message struct {
    Role    string            `json:"role"`
    Content string            `json:"content"`
    ID      string            `json:"id,omitempty"`        // 新增：消息级 id，供 evidence 引用
    Ts      time.Time         `json:"ts,omitempty"`        // 新增：时间戳
    Meta    map[string]string `json:"meta,omitempty"`      // 新增：扩展标记
}
```
- MySQL `chat_messages` 表加 `msg_id`（可空）；`sessions` 表加 `last_active_at DATETIME`、`summarized TINYINT DEFAULT 0`。
- evidence 若暂不加 `msg_id`，MVP 可用 `session_id + 消息序号` 作引用，字段先留空不阻塞。

### 3.2 L2 GORM 模型（`entry.go`）

```go
type MemoryEntry struct {
    ID          int64  `gorm:"primaryKey;autoIncrement"`
    UserID      int64  `gorm:"index:idx_user_channel;index:idx_user_key;not null"`
    AgentID     string `gorm:"type:varchar(36);not null;default:'';uniqueIndex:uk_uakv"` // '' 而非 NULL
    Channel     string `gorm:"type:varchar(16);index:idx_user_channel;not null"`         // fact|policy
    Kind        string `gorm:"type:varchar(16);not null"`                                // fact/preference/policy/summary
    KeyName     string `gorm:"type:varchar(128);index:idx_user_key;not null;uniqueIndex:uk_uakv"`
    Value       string `gorm:"type:text;not null"`
    ValueHash   string `gorm:"type:char(32);not null;uniqueIndex:uk_uakv"`
    Cardinality string `gorm:"type:varchar(8);default:'multi'"`                          // single|multi
    Confidence  float64 `gorm:"not null;default:1.0"`
    Evidence    string `gorm:"type:json"`                                                // [{session_id,message_id}]
    Status      string `gorm:"type:varchar(8);default:'active'"`                         // active|archived
    Source      string `gorm:"type:varchar(16);default:'rule'"`                          // rule|user_stated|llm_extracted
    HitCount    int    `gorm:"default:0"`
    LastUsedAt  *time.Time
    CreatedAt   time.Time
    UpdatedAt   time.Time
    ExpiresAt   *time.Time
}
func (MemoryEntry) TableName() string { return "memory_entries" }

type SessionSummary struct {
    ID        int64 `gorm:"primaryKey;autoIncrement"`
    SessionID string `gorm:"type:varchar(36);index"`
    UserID    int64  `gorm:"index"`
    Summary   string `gorm:"type:text"`
    Topics    string `gorm:"type:json"`
    CreatedAt time.Time
}
```
- 唯一键 `uk_uakv = (user_id, agent_id, key_name, value_hash)`，GORM `uniqueIndex:uk_uakv` 复合。
- `AutoMigrate(&MemoryEntry{}, &SessionSummary{})` 在各 store 构造函数内执行（与现有 `NewMySQLStore` 风格一致）。

---

## 四、组件详设

### 4.1 L1 — TaskState 状态机（`taskstate.go` / `taskstore.go`）

```go
type Step struct{ Desc, Status, Tool string } // status: todo|doing|done|failed
type TaskState struct {
    Goal    string
    Stage   string            // planning|executing|confirming|done
    Steps   []Step
    Pending []string
    Slots   map[string]string
}

// 规则驱动，零 LLM。在 ReAct 循环内按事件调用。
func updateTaskState(ts *TaskState, ev TaskEvent) // ev: 用户消息 / tool_call / tool 结果 / 最终回复

type TaskStore interface {
    Load(ctx context.Context, sessionID string) *TaskState   // miss 返回空状态，不报错
    Save(ctx context.Context, sessionID string, ts *TaskState) error
    Delete(ctx context.Context, sessionID string) error
}
// memTaskStore: sync.Map + TTL（单实例）；redisTaskStore: HSET wm:{sid} + EXPIRE（多实例）
```
- 注入：`renderTaskState(ts)` 产出「## 当前任务」文本段。
- 生命周期：`Load` 于 `ProcessStream` 开头；`Save` 于结束；`Delete` 随 session 删除级联。

### 4.2 L2 事实通道 — FactStore（`fact_store.go`）

```go
type RecallQuery struct {
    UserID   int64
    AgentID  string
    Kinds    []string   // fact/preference
    Keywords []string   // 来自 QueryRouter 抽取
    TopK     int
    MinScore float64
}
type ScoredEntry struct{ Entry MemoryEntry; Score float64 }

type FactStore interface {
    Admit(ctx context.Context, e MemoryEntry, ac AdmissionConfig) (bool, error)
    Recall(ctx context.Context, q RecallQuery) ([]ScoredEntry, error)
    Get(ctx context.Context, userID int64, key string) ([]MemoryEntry, error)
    MarkUsedAsync(ids []int64)                 // 投递到 buffered channel，后台批量 UPDATE
    Evolve(ctx context.Context, userID int64, now time.Time) (EvolveStat, error)
}
```

**Admit（admission control）实现顺序**：
```
1) 门禁：len(evidence)>0 && confidence >= ac.WriteThreshold        否则 return false
2) 去重：按 uk_uakv 查；命中→按 cardinality 合并（single 覆盖旧值置 archived / multi 幂等跳过）
3) 配额：COUNT(user active) >= ac.MaxEntriesPerUser → 删最低 score 一条再插
4) Upsert（clause.OnConflict uk_uakv DoUpdates）
```

**Recall（读取 + scoring，纯 SQL + 内存排序）**：
```
SELECT ... WHERE user_id=? AND agent_id=? AND channel='fact' AND status='active'
          AND kind IN (?) [AND (key_name IN keywords OR value LIKE ...)]
内存计算 score = 0.3*confidence + 0.2*freshness + 0.5*relevance
  freshness = decay^days_since(COALESCE(last_used_at, created_at))
  relevance = key 精确命中?1.0 : (kind/keyword 部分命中?0.5:0)
过滤 score>=MinScore，取 TopK；返回后 MarkUsedAsync(ids)
```

### 4.3 L2 策略通道 — PolicyStore（`policy_store.go`）
同表 `channel='policy'`，`Value` 存 JSON `{condition, action}`。
```go
type PolicyStore interface {
    Admit(ctx context.Context, p MemoryEntry, ac AdmissionConfig) (bool, error)
    Match(ctx context.Context, userID int64, feats QueryFeatures) ([]MemoryEntry, error) // 条件匹配 + hit_count desc
    HitAsync(ids []int64)
}
```

### 4.4 L2 向量通道 — VectorStore / EmbeddingProvider（Phase 3，`vector_store.go`）
- 复用顶层 `cfg.Milvus`（`Address/Collection/TextMaxLength` 已存在）。
- `enabled=false` 时 `MemoryManager.vector == nil`，读写整段跳过，不引入 Milvus 依赖到主流程。
- `EmbeddingProvider` 首版对接 OpenAI/DeepSeek embedding 端点，复用 `ModelConfigStore` 的 base_url/token。

### 4.5 确定性提取器（`extractor.go`，主路径 · 零 LLM）
```go
type DeterministicExtractor struct{ rules []Rule }
func (x *DeterministicExtractor) Extract(sessionID string, delta []Message) []MemoryEntry
```
来源：
- **tool args**：解析本轮 `RoleTool`/`RoleFunction` 消息里的工具参数 → `fact`（如 `weather(city=北京)` → `user.mentioned_city=北京`，source=`rule`, confidence=0.9）。
- **正则规则**：`我叫X / 我是X / 我在X / 我的邮箱是X` → `user.name/user.city/...`。
- **显式命令**：`记住…` → 高置信 fact（source=`user_stated`,1.0）；`忘掉…` → 删除/归档。
- **工具偏好**：统计本会话工具选择 → `policy`。
产出统一走 `Admit` 门禁。

### 4.6 LLM 摘要器（`summarizer.go`，次路径 · 每会话≤1）
- 由 `IdleScanner` 触发，读全会话 → 一次 LLM（复用 `llm.Provider` 或 `extraction_model` override）→ 结构化 JSON（摘要+事实+偏好+策略，每条带 confidence/cardinality/evidence）→ `Admit`。
- 受 `max_extractions_per_session` 限流；完成置 `sessions.summarized=1`。

### 4.7 QueryRouter（`router.go`，确定性）
```go
type QueryClass int // ClassFact | ClassContinue | ClassSemantic | ClassDefault
type QueryFeatures struct{ Keywords []string; Class QueryClass }
func (r *QueryRouter) Route(msg string) QueryFeatures // 疑问词/指代/延续词/记忆词 命中即分类，零 LLM
```
路由 → 选通道：Fact / (TaskState+Policy) / (Vector+Fact) / (Fact+Policy)。

### 4.8 MemoryManager（`manager.go`，编排）
```go
type MemoryManager struct {
    chat    SessionStore
    task    TaskStore
    fact    FactStore
    policy  PolicyStore
    vector  VectorStore        // 可 nil
    embed   EmbeddingProvider  // 可 nil
    router  *QueryRouter
    extract *DeterministicExtractor
    summ    *LLMSummarizer
    cfg     LongTermConfig
    llog    *zap.Logger
}

// L1 转发（engine 只持有 memMgr，taskStore 字段不可导出，故经此转发）
func (m *MemoryManager) Load(ctx context.Context, sessionID string) *TaskState   // 委托 task.Load
func (m *MemoryManager) Save(ctx context.Context, sessionID string, ts *TaskState) // 委托 task.Save，nil 时 no-op

// 读取：并行、限时、评分、budget 截断，返回拼接好的增强文本段（供拼进 systemPrompt）
func (m *MemoryManager) Retrieve(ctx context.Context, userID int64, agentID, sessionID, userMsg string, ts *TaskState) string

// 写入主路径（每轮，异步、确定性）
func (m *MemoryManager) OnTurnEnd(ctx context.Context, userID int64, sessionID string, delta []Message)

// 写入次路径（静默触发，LLM 摘要，每会话≤1）
func (m *MemoryManager) OnIdle(ctx context.Context, userID int64, sessionID string)
```
`Retrieve` 内部用 `errgroup` + `context.WithTimeout`（fact/policy 200ms，vector 500ms），失败通道跳过，各段按固定顺序与 `budget_ratio` 截断拼接。

---

## 五、Engine 集成（精确改动点）

### 5.1 新增字段与 option
```go
type Engine struct { /* ...existing... */ memMgr *memory.MemoryManager }
func WithMemoryManager(mm *memory.MemoryManager) EngineOption { return func(e *Engine){ e.memMgr = mm } }
```
`memMgr == nil` 时全部记忆逻辑短路——**保证未接入时行为与现状完全一致**。

### 5.2 `ProcessStream` 内改动（对照现有行号）

```go
// 现有 L136 之后、L142 之前：载入上一轮任务状态 + 起一个本轮消息累加器
var task *memory.TaskState
var turnDelta []memory.Message // 本轮新增消息，供 OnTurnEnd 确定性提取
if e.memMgr != nil { task = e.memMgr.Load(ctx, sessionID) }
turnDelta = append(turnDelta, memory.Message{Role: models.RoleHuman, Content: userMessage})

// 现有 L142 History() 之后、L146 buildMessages 之前：注入增强（改 prompt 字符串）
const memAgentID = "" // MVP：一律用户级记忆；agent 级隔离见下方说明
if e.memMgr != nil {
    enrich := e.memMgr.Retrieve(ctx, userID, memAgentID, sessionID, userMessage, task)
    prompt = appendEnrichment(prompt, enrich) // Base Prompt 置顶 + 追加增强段
}
messages := e.buildMessages(prompt, history) // 不改 buildMessages 本身

// ReAct 循环内（L192-200 工具执行后）：规则更新 task + 累加工具消息
if e.memMgr != nil && task != nil {
    memory.UpdateTaskState(task, toolEvent) // tool_call / tool 结果事件
    turnDelta = append(turnDelta, toolMsgs...)
}

// 无工具最终回复分支（L177/L181 done 前）与兜底最终回复分支（L218/L222 done 前）：
//   先补「最终回复→Stage=done」转移，再回写 task + 触发主路径提取
if e.memMgr != nil && task != nil {
    memory.UpdateTaskState(task, memory.FinalReplyEvent(result.Content)) // 补 done 转移
    turnDelta = append(turnDelta, memory.Message{Role: models.RoleAI, Content: result.Content})
    e.memMgr.Save(ctx, sessionID, task)
    go e.memMgr.OnTurnEnd(context.WithoutCancel(ctx), userID, sessionID, turnDelta)
}
```
- **agentID（F1）**：`AgentConfig` 现无 ID 字段、`ProcessStream` 也无 agentID 参数，故 MVP 用常量 `memAgentID=""` → `memory_entries.agent_id=''`（与 §3.2「`''` 而非 NULL」自洽，即用户级记忆）。若将来要 agent 级隔离，须给 `ProcessStream` 或 `AgentConfig` 显式加 agentID 字段后再改此常量为该值——**不在 MVP 范围**。
- **turnDelta（F3）**：engine 逐条 `AddMessage` 落库、不保留本轮切片，故在 `ProcessStream` 内显式累加本轮消息（用户消息 + 工具消息 + 最终回复），一次性传给 `OnTurnEnd`，避免其再回读增量。
- **done 转移（F4）**：`UpdateTaskState` 除循环内工具事件外，还须在**两处最终回复分支**各调一次 `FinalReplyEvent`，落实 proposal「最终回复(无 tool_call)→ `Stage=done`」；否则无工具直答路径永不置 done。
- `appendEnrichment(prompt, enrich)`：`enrich` 为空返回原 `prompt`；非空则 `prompt + "\n\n" + enrich`。
- `context.WithoutCancel`：请求 ctx 结束后异步提取仍需存活（Go 1.21+）。
- L1 注入用**载入的上一轮 state**，正确无空注入；循环内 `UpdateTaskState` 供**下一轮**用。

---

## 六、配置改动（`config.go`）

```go
type MemoryConfig struct {
    Type        string        `mapstructure:"type"`
    TTL         time.Duration `mapstructure:"ttl"`
    MaxMessages int           `mapstructure:"max_messages"`
    Task        TaskMemConfig     `mapstructure:"task"`      // 新增
    LongTerm    LongTermConfig    `mapstructure:"longterm"`  // 新增
    Evolution   EvolutionConfig   `mapstructure:"evolution"` // 新增
}
type TaskMemConfig struct {
    Backend string        `mapstructure:"backend"` // memory|redis
    IdleTTL time.Duration `mapstructure:"idle_ttl"`
}
type LongTermConfig struct {
    Enabled     bool            `mapstructure:"enabled"`
    Router      bool            `mapstructure:"router"`
    Fact        ChannelConfig   `mapstructure:"fact"`
    Policy      ChannelConfig   `mapstructure:"policy"`
    Vector      VectorConfig    `mapstructure:"vector"`
    Scoring     ScoreWeights    `mapstructure:"scoring"`
    BudgetRatio float64         `mapstructure:"budget_ratio"`
    Admission   AdmissionConfig `mapstructure:"admission"`
    ExtractionModel string      `mapstructure:"extraction_model"`
}
type AdmissionConfig struct {
    WriteThreshold           float64 `mapstructure:"write_threshold"`
    MaxEntriesPerUser        int     `mapstructure:"max_entries_per_user"`
    MaxExtractionsPerSession int     `mapstructure:"max_extractions_per_session"`
}
// ChannelConfig{Enabled,TopK,MinScore} · VectorConfig{Enabled,EmbeddingDim,TopK,MinSimilarity} · ScoreWeights{Confidence,Freshness,Relevance} · EvolutionConfig{Enabled,DecayFactor,IdleScanCron,CleanupCron}
```
`applyDefaults()` 补默认：`task.backend=memory / idle_ttl=5m`；`longterm.enabled=true / router=true / budget_ratio=0.2`；`admission.write_threshold=0.6 / max_entries_per_user=1000 / max_extractions_per_session=1`；`scoring=0.3/0.2/0.5`；`vector.enabled=false`；`evolution.decay_factor=0.95 / idle_scan_cron="*/1 * * * *" / cleanup_cron="0 3 * * *"`。向量通道 Milvus 连接复用顶层 `cfg.Milvus`。

---

## 七、启动装配（`main.go`）

```go
// sessionStore 之后
var memMgr *memory.MemoryManager
if cfg.Memory.LongTerm.Enabled {
    factStore, _ := memory.NewFactStore(gormDB)      // AutoMigrate memory_entries
    policyStore, _ := memory.NewPolicyStore(gormDB)
    taskStore := memory.NewTaskStore(&cfg.Memory, redisClient) // memory|redis；Phase 1 backend=memory 时 redisClient 传 nil
    var vec memory.VectorStore                        // 默认 nil
    if cfg.Memory.LongTerm.Vector.Enabled { vec, _ = memory.NewMilvusVectorStore(&cfg.Milvus) }
    memMgr = memory.NewMemoryManager(sessionStore, taskStore, factStore, policyStore, vec,
        llmProvider, &cfg.Memory.LongTerm, appLogger)

    // 后端无关静默扫描（触发 LLM 次路径）
    scanner := memory.NewIdleScanner(gormDB, memMgr, &cfg.Memory)
    go scanner.Start(context.Background())
}

engine := agent.New(llmProvider, reg, sessionStore, cb,
    /* ...existing opts... */
    agent.WithMemoryManager(memMgr), // nil 时行为同现状
)
```
`router.Setup(...)` 签名不变（memMgr 只进 engine）。

> **作用域提示（F5）**：现有 redis client 建于 `initSessionStore` 内部，非顶层变量。Phase 1 `task.backend=memory`，`redisClient` 传 `nil` 即可；一旦启用 `task.backend=redis`，需把 redis client 构造提升为 `main` 顶层变量再传入 `NewTaskStore`。

---

## 八、三链路时序（落到真实调用）

**写入**：`ProcessStream` → L0 `mem.AddMessage`（现有，同步）→ 循环内 `UpdateTaskState`（内存）→ done 前 `Save(task)` + `go OnTurnEnd`（确定性 `Extract`→`Admit`，异步）。LLM 次路径不在此发生。

**读取**：`ProcessStream` 进入循环前 → `Retrieve`（`router.Route` → 并行 `fact.Recall`/`policy.Match`/`vector.Search` 限时 → 评分 → budget 截断 → 文本段）→ `appendEnrichment` 进 prompt → `buildMessages`。

**演化**：`IdleScanner`（cron 扫 `sessions.last_active_at<now-idle_ttl && !summarized`）→ `OnIdle` → `LLMSummarizer`（≤1 LLM/会话）→ `Admit`；`cleanup_cron` → `fact.Evolve`（衰减/物删/policy 淘汰）。

---

## 九、并发 / 错误 / 可观测性

- **异步存活**：`OnTurnEnd` 用 `context.WithoutCancel(ctx)`；panic recover 包裹 goroutine（对齐现有 `ProcessStream` 的 recover 风格）。
- **不阻塞主流程**：所有 L2 错误仅 `logger.L().Error(..., tracing.ErrFields(ctx, err)...)`，绝不影响 SSE 输出。
- **MarkUsedAsync/HitAsync**：buffered channel + 后台 goroutine 批量 `UPDATE`，读路径零写等待。
- **重试补偿**：`OnTurnEnd`/`OnIdle` 内指数退避 3 次；仍失败落 `memory_tasks` 表；`IdleScanner` 全量摘要天然补齐遗漏增量。
- **可观测**：复用 `tracing.ZapFields(ctx)` 打点；新增指标日志（提取条数、命中率、平均 confidence）。

---

## 十、测试方案

| 层 | 测试 |
|----|------|
| L1 | `updateTaskState` 表驱动单测：各事件→状态转移；`memTaskStore`/`redisTaskStore` load/save/TTL |
| L2 Fact | `Admit` 四步（门禁/去重/配额/幂等）；单值覆盖归档 vs 多值并存；`Recall` 评分排序与 TopK/MinScore |
| Extractor | 正则/tool args/显式命令 → 期望 MemoryEntry；零 LLM 断言 |
| Router | 各 QueryClass 分类样例（含中文疑问/延续/记忆词） |
| Manager | `Retrieve` 超时降级（mock 慢 store）；budget 截断；`enabled=false` 短路 |
| 集成 | `ProcessStream` + mock LLM：验证增强注入进 system prompt、跨轮 task 保持、`memMgr=nil` 行为同现状 |
| 迁移 | `AutoMigrate` 幂等；唯一键在 `agent_id=''` 下生效（插重复被去重） |

`go test ./internal/memory/... ./internal/agent/...`；沿用现有表驱动风格。

---

## 十一、Phase 1 落地清单（文件级）

1. `interface.go`：`Message` 加 `ID/Ts/Meta`；`mysql.go` 的 sessions 模型加 `last_active_at/summarized`。
2. `taskstate.go` + `taskstore.go`：状态机 + memTaskStore（Phase 1 仅内存）。
3. `entry.go` + `fact_store.go`：`MemoryEntry` GORM + `NewFactStore`（AutoMigrate）+ `Admit`/`Recall`/`MarkUsedAsync`。
4. `extractor.go`：确定性提取（tool args + 正则 + 显式命令）。
5. `router.go`：`QueryRouter`（fact/default 两类即可起步）。
6. `manager.go`：`MemoryManager`（fact-only：`Retrieve` + `OnTurnEnd`）。
7. `idle_scan.go`：扫描器骨架（Phase 1 预留触发点，摘要器 Phase 2 接入）。
8. `config.go`：`MemoryConfig` 扩展 + `applyDefaults`。
9. `engine.go`：`WithMemoryManager` + 5.2 注入/回写/触发改动。
10. `main.go`：构造 fact/task store + manager + 装配。
11. 单测：L1 状态机、Fact `Admit`/`Recall`、Router、Manager 短路与集成。

**Phase 1 完成即可跑通**：跨会话确定性事实记忆（无任何额外 LLM 调用），读取按 query 路由注入 system prompt。Policy/LLM 摘要/向量通道按 proposal 分期 Phase 2、3 顺次接入。
