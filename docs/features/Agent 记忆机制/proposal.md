# Agent 通用记忆机制 — 需求与设计文档

> 项目：ansmeee-ai-agent  
> 状态：Proposal（待评审 · v3）

---

## 一、背景与目标

### 现状

当前系统的记忆能力等同于一个无状态的「聊天记录本」：

| 能力 | 现状 |
|------|------|
| 对话存储 | `SessionStore` 接口 + InMemory/Redis/MySQL 三套实现 |
| 上下文管理 | `trimHistory()` 按条数截断（默认 50 条），仅保证 tool_call/tool 配对不被拆分 |
| 跨会话记忆 | 无 — 每个 session 互相隔离，用户/Agent 没有持久化的知识积累 |
| 语义检索 | 无 — Milvus 配置已声明但未接入 |
| 行为学习 | 无 — 不具备事实提取、偏好学习、策略沉淀能力 |

### 目标

设计一套 **三层记忆架构**，让 Agent 具备：

1. **短期精确回忆** — 当前对话的完整上下文（L0）
2. **会话级任务状态** — 有界确定性任务状态机，零额外 LLM 成本（L1）
3. **长期可控记忆** — 跨会话事实/偏好/策略，三通道分明、确定性提取为主、写入有门禁、读取按 query 路由（L2）

### 设计演进（v1→v3）

| 版本 | 关键变化 |
|------|---------|
| v1 | 五层（L0–L4），含独立向量层 + 知识图谱层 |
| v2 | 收敛为三层：L2 合并结构化/向量/策略；写入加 evidence+confidence 门控；读取加 scoring |
| **v3** | **① L2 显式拆分「事实/策略/向量」三条逻辑通道；② 确定性提取（tool args/规则）升为写入主路径，LLM 提取降为每会话≤1 次的次路径；③ 读取引入 query-aware routing；④ L1 升级为有界 Task State Machine；⑤ 引入 admission control（配额+限流+门禁）** |

**核心取舍**：所有「每轮都要跑」的记忆逻辑（L1 更新、L2 提取主路径、query 路由）**一律确定性、零 LLM**；LLM 只出现在「每会话≤1 次」的静默摘要，且受限流门禁约束。既控幻觉又控成本。

---

## 二、三层记忆架构

```
┌──────────────────────────────────────────────────────────────┐
│  L2  长期记忆  Long-term Memory（三通道）                     │
│   ┌─────────────┬─────────────┬─────────────────────────┐    │
│   │ 事实通道     │ 策略通道     │ 向量通道（可选/默认关）  │    │
│   │ Fact        │ Policy      │ Vector                  │    │
│   │ 精确 K-V     │ 行为偏好     │ 语义召回                 │    │
│   └─────────────┴─────────────┴─────────────────────────┘    │
│   写入：确定性提取(主)+LLM摘要(次) · 门禁 admission control    │
│   读取：query router → 选通道 → 通道内 scoring+top-k+budget    │
├──────────────────────────────────────────────────────────────┤
│  L1  工作记忆  Task State Machine                            │
│      goal / stage / steps / pending / slots · 规则驱动零 LLM  │
├──────────────────────────────────────────────────────────────┤
│  L0  对话层  Chat Buffer                                      │
│      完整消息序列 · 角色/时间戳 · 会话元数据（现有 SessionStore）│
└──────────────────────────────────────────────────────────────┘
```

### L0：对话层（Chat Buffer）

**定位**：忠实记录，不丢信息，是所有上层记忆的唯一「事实源」。

| 属性 | 说明 |
|------|------|
| 存储内容 | 完整 Message 序列（role / content / tool_call / metadata / timestamp） |
| 存储介质 | MySQL（持久化）、Redis（热缓存）、InMemory（开发/测试） |
| 生命周期 | 跟随 session，用户可删除 |
| 与现有系统关系 | **即当前 `SessionStore` 接口的完整实现，只增不改** |

**改造要点**：
- `Message` 新增 `ID`（消息级唯一 id，供 evidence 引用）、`Timestamp`、`Metadata`
- `sessions` 表新增 `last_active_at`、`summarized`（后端无关的静默摘要调度需要，见写入链路）

### L1：工作记忆（Task State Machine）

**定位**：会话级、有界、**确定性**的任务状态机。取代 v2 的扁平 slot map，让 Agent 在多轮 ReAct 中保持「目标—阶段—步骤」的结构化焦点。**全程规则驱动，零额外 LLM 调用**。

**核心结构**：
```go
type TaskState struct {
    Goal    string            // 任务目标（首条用户消息 / 显式声明）
    Stage   string            // planning | executing | confirming | done
    Steps   []Step            // 有序步骤
    Pending []string          // 待确认事项
    Slots   map[string]string // 附属键值（工具参数/结果摘要），保留原 slot 能力
}
type Step struct {
    Desc   string
    Status string // todo | doing | done | failed
    Tool   string // 关联工具（可选）
}
```

**状态转移（规则，无 LLM）**：

| 事件 | 转移 |
|------|------|
| 首条用户消息 | `Goal` = 消息；`Stage=planning` |
| 出现 tool_call | 追加/更新 `Step{doing, Tool}`；`Stage=executing` |
| tool 返回成功/失败 | 对应 `Step.Status=done/failed`；结果摘要入 `Slots` |
| LLM 产出最终回复（无 tool_call） | `Stage=done` |
| 输出含 `需确认:`/`TODO:` | 追加 `Pending` |

**跨轮持久化（解决 v2 的「每请求清零」问题）**：
- 每次 `ProcessStream` **先从 store 载入** `TaskState`（key=`task:{sessionID}`），再在 ReAct 循环内更新，结束**回写** store。
- 单实例默认内存 store；多实例用 Redis（见 §七）。
- 注入时机：**循环开始前**注入「上一轮载入的」`TaskState`，**循环内每轮**用最新 state 重注入——避免 v2「注入在填充之前恒为空」的错位。

### L2：长期记忆（三条逻辑通道）

**定位**：跨会话、可控的用户知识库。**显式拆分三条逻辑通道**，各有独立读写逻辑，杜绝黑盒：

| 通道 | 存储内容 | 介质 | 默认 | 读取方式 |
|------|---------|------|------|---------|
| **事实通道 Fact** | 事实 / 偏好（key-value） | MySQL | 开 | 精确查 + kind 过滤 |
| **策略通道 Policy** | 工具/行为偏好规则 | MySQL（同表 `kind='policy'`，独立模块逻辑） | 开 | 条件匹配 + hit 排序 |
| **向量通道 Vector** | 原文/摘要 embedding | Milvus | **关** | top-k 语义相似 |

> 事实与策略共用物理表 `memory_entries`（减少表数量），但在代码层是两个独立通道模块（不同的写入门禁、读取策略、演化规则）。向量通道物理独立。

**数据模型**：
```sql
CREATE TABLE memory_entries (
    id           BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id      BIGINT NOT NULL,
    agent_id     VARCHAR(36) NOT NULL DEFAULT '',  -- 用 '' 而非 NULL，保证唯一键在用户级记忆下生效
    channel      VARCHAR(16) NOT NULL,             -- fact | policy（逻辑通道）
    kind         VARCHAR(16) NOT NULL,             -- fact / preference / policy / summary
    key_name     VARCHAR(128) NOT NULL,
    value        TEXT NOT NULL,                    -- 纯文本或 JSON（policy 存 {condition, action}）
    value_hash   CHAR(32) NOT NULL,               -- md5(value)，多值键去重
    cardinality  VARCHAR(8) DEFAULT 'multi',       -- single / multi
    confidence   FLOAT NOT NULL DEFAULT 1.0,
    evidence     JSON,                            -- [{session_id, message_id}]，防幻觉可追溯
    status       VARCHAR(8) DEFAULT 'active',      -- active / archived
    source       VARCHAR(16) DEFAULT 'rule',       -- rule / user_stated / llm_extracted
    hit_count    INT DEFAULT 0,
    last_used_at DATETIME,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    expires_at   DATETIME,
    INDEX idx_user_channel (user_id, channel),
    INDEX idx_user_key (user_id, key_name),
    UNIQUE KEY uk_user_agent_key_val (user_id, agent_id, key_name, value_hash)
);

CREATE TABLE session_summaries (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    session_id VARCHAR(36) NOT NULL, user_id BIGINT NOT NULL,
    summary TEXT NOT NULL, topics JSON, created_at DATETIME NOT NULL,
    INDEX idx_user (user_id), INDEX idx_session (session_id)
);
```

**Go 接口（按通道拆分）**：
```go
// 事实通道
type FactStore interface {
    Admit(ctx context.Context, e MemoryEntry) (bool, error)   // 门禁：evidence+confidence+去重+配额
    Recall(ctx context.Context, q RecallQuery) ([]ScoredEntry, error)
    Get(ctx context.Context, userID int64, key string) ([]MemoryEntry, error) // 多值键返回多条
    MarkUsedAsync(ids []int64)                                // fire-and-forget，不阻塞读路径
    Evolve(ctx context.Context, userID int64, now time.Time) (EvolveStat, error)
}
// 策略通道
type PolicyStore interface {
    Admit(ctx context.Context, p PolicyEntry) (bool, error)
    Match(ctx context.Context, userID int64, feats QueryFeatures) ([]PolicyEntry, error)
    Hit(ids []int64)                                          // 命中计数，异步
}
// 向量通道（可选，enabled=false 时为 nil）
type VectorStore interface {
    StoreBatch(ctx context.Context, docs []VectorDocument) error
    Search(ctx context.Context, userID int64, emb []float32, topK int) ([]VectorSearchResult, error)
    DeleteBySession(ctx context.Context, sessionID string) error
}
type EmbeddingProvider interface { Embed(ctx context.Context, texts []string) ([][]float32, error) }
```

**向量 Collection（可选）**：`agent_memory_embeddings`：`id · user_id · session_id · kind · content · embedding(FLOAT_VECTOR) · created_at`，`IVF_FLAT/nlist=128`。Embedding 复用用户 LLM Provider 的 embedding 接口，经 `ModelConfigStore` 配置。

---

## 三、三条核心链路

### 链路 1：写入链路（确定性为主 + LLM 为次 + admission control）

```
每轮请求 / 消息事件
    │  同步
    ├─▶ L0  AddMessage() 原文持久化（唯一事实源）
    │
    ├─▶ L1  TaskState 规则更新（goal/stage/steps/slots），零 LLM
    │
    └─▶ L2  写入（两条路径，都过 admission control 门禁）
         ├─ 主路径·确定性提取（每轮，零 LLM）
         │    • tool args → fact（如 city="北京"）
         │    • 规则/正则（"我叫X"、"我在Y"）→ fact
         │    • 显式命令 "记住…/忘掉…" → fact/删除
         │    • 工具选择偏好 → policy
         └─ 次路径·LLM 摘要提取（每会话≤1 次，静默触发）
              • 全会话一次 LLM，抽事实/偏好/策略 + 摘要
              • 受限流：每 session LLM 抽取次数上限
```

**写入时机**：

| 层级 | 触发 | 同步/异步 | LLM |
|------|------|----------|-----|
| L0 | 每条消息 | 同步 | 无 |
| L1 | ReAct 每轮 | 同步（内存/Redis） | 无 |
| L2 主路径 | 每轮请求结束 | 异步 | **无（确定性）** |
| L2 次路径 | 静默 TTL 到期 | 异步 | 每会话 1 次 |

**admission control（配额 + 限流 + 门禁）**：
```
admit(entry) 依次校验：
  1) 门禁：evidence 非空 ∧ confidence ≥ write_threshold(默认0.6)   否则丢弃
  2) 去重：命中唯一键 → 按单/多值键规则合并，不新增
  3) 配额：user 条目数 ≥ max_entries_per_user → 淘汰最低分条目再插
  4) 限流：仅约束 LLM 次路径——本 session 抽取次数 ≥ max_extractions → 跳过
```
- `source` 初始置信度：`user_stated` 1.0 > `rule` 0.9 > `llm_extracted` 0.7；确定性主路径天然高置信、易过门禁，LLM 次路径更严。

**后端无关的静默触发（修正 v2「依赖 InMemory goroutine」）**：
- 不再复用某个后端专属的 cleanup goroutine。改为**独立定时任务**（cron，如每 1min）扫描 `sessions` 表：`last_active_at < now - idle_ttl ∧ summarized=false` → 触发全量摘要，完成后置 `summarized=true`。
- 对 MySQL/Redis/InMemory **所有后端一致生效**。Redis keyspace 过期通知可作为低延迟可选优化。

### 链路 2：读取链路（query-aware routing + 通道内 scoring）

**不再对所有记忆统一 scoring**，先由确定性路由器按 query 类型选通道，再在通道内排序：

```
用户消息 → QueryRouter（规则，零 LLM）→ 选通道
    │
    ├─ 事实型（"我的城市?/我叫什么"）      → Fact 精确查
    ├─ 任务延续型（"继续/接着刚才"）        → L1 TaskState + Policy
    ├─ 语义/开放型（"跟X相关的…"）         → Vector（若启用）+ Fact
    └─ 默认                               → Fact + Policy
        │
        ▼ 各选中通道内：scoring → top-k → 合并 → budget 截断 → 注入
```

**QueryRouter 分类特征（确定性）**：疑问词（谁/什么/哪）、指代（我的/你）、延续词（继续/接着/还有）、显式记忆词（记得/之前说过）；命中即路由，无匹配走默认。**不调用 LLM**。

**通道内 scoring**：
```
score = w_c·confidence + w_f·freshness + w_r·relevance    (w_c=0.3, w_f=0.2, w_r=0.5)
  freshness = decay_factor ^ days_since(last_used_at ?? created_at)   // NULL 回退 created_at
  relevance:
    Fact   —  key 精确命中=1.0 / kind 或关键词部分命中=0.5 / 否则 0
    Vector —  余弦相似度 [0,1]
```
- 仅注入 `score ≥ min_score` 的 top-k。命中条目 `MarkUsedAsync`（异步 fire-and-forget，不阻塞读路径）。
- **避免双重衰减**：读取评分的 `freshness` 只用于「排序」；演化链路的 `confidence` 物理衰减只用于「遗忘阈值」。二者目的不同，且 `w_f` 仅 0.2、置信度衰减阈值 0.1，重叠影响可控——文档明确二者不叠加惩罚同一目的。

**Prompt 结构与合并规则**：
```
[System] Base Prompt（置顶完整保留）
  ├─ ## 用户信息      ← Fact top-k
  ├─ ## 行为偏好      ← Policy 命中
  ├─ ## 当前任务      ← L1 TaskState（goal/stage/pending）
  └─ ## 相关历史      ← Vector top-k（可选）
[History] L0 trimmed
[Current User Message]
```
- 追加而非替换；固定顺序（利于前缀缓存）；空段省略；**memory budget** 增强段占上下文上限（默认 20%），超限按 用户信息>行为偏好>任务>历史 截断。

**分层降级（读取不阻塞）**：

| 通道 | 超时 | 失败 |
|------|------|------|
| L0 | 必成 | 报错终止 |
| L1 | 内存/Redis | 空状态 |
| Fact/Policy | 200ms | 跳过 |
| Vector | 500ms（embedding+检索合并计时；首轮 History 空则跳过） | 跳过 |

各通道独立 `context.WithTimeout` + `errgroup` 汇聚，互不拖累。

### 链路 3：演化链路（Evolution）

```
触发：静默 TTL（后端无关扫描）/ 每日 cron / 配额超限 / 用户显式
   ├─ 摘要压缩（L0→L2 次路径 LLM，每会话1次）
   ├─ 合并去重（单值键覆盖归档 / 多值键 value_hash 并存）
   └─ 衰减遗忘（confidence 指数衰减；<0.1 或过期物删；policy 长期0命中淘汰；向量随源删）
```

**单值 vs 多值（避免静默丢数据）**：单值键（`user.current_city`）新值覆盖、旧值 `archived`+降置信；多值键（`user.visited_city`）按 `value_hash` 并存永不覆盖。`cardinality` 由确定性规则或 LLM 摘要标注，默认 `multi`（保守）。

---

## 四、系统集成设计

### 编排器
```go
type MemoryManager struct {
    chatStore   SessionStore   // L0，已有
    taskStore   TaskStore      // L1 持久化（内存/Redis）
    fact        FactStore      // L2 事实通道
    policy      PolicyStore    // L2 策略通道
    vector      VectorStore    // L2 向量通道，可选（nil=关）
    embedder    EmbeddingProvider
    router      QueryRouter    // 读取路由，确定性
}
```

### 与 Engine 集成（伪代码，已修正 v2 时序/命名问题）
```go
func (e *Engine) ProcessStream(ctx context.Context, agentCfg AgentConfig,
    sessionID, userMessage string, userID int64) <-chan StreamEvent {
    go func() {
        // L1: 载入上一轮任务状态（跨轮持久化）
        task := e.memMgr.taskStore.Load(ctx, sessionID)

        // L0: 同步写入用户消息
        e.memMgr.chatStore.AddMessage(ctx, sessionID, userMessage, userID)

        // L2 读取：query 路由 → 选通道 → 评分召回
        history := e.memMgr.chatStore.History(ctx, sessionID)
        enrich := e.memMgr.Retrieve(ctx, userID, sessionID, userMessage) // 内部 router+scoring

        for iter := 0; iter < maxIter; iter++ {              // ReAct，现有逻辑
            msgs := injectEnrichment(e.buildMessages(agentCfg, history), enrich, task) // 每轮用最新 task 注入
            result := llm.ChatStream(ctx, msgs, tools)
            // ... tool execution ...
            task = updateTaskState(task, result)             // L1 规则更新，零 LLM
        }

        e.memMgr.taskStore.Save(ctx, sessionID, task)        // L1 回写
        go e.memMgr.OnTurnEnd(ctx, sessionID, userID)        // L2 主路径确定性提取（异步）
        // L2 次路径（LLM 摘要）由后端无关的静默扫描触发，不在每轮做
    }()
}
```

### 配置扩展
```yaml
memory:
  type: mysql
  ttl: 30m
  max_messages: 100

  task:                       # L1
    backend: memory           # memory | redis；多实例必须 redis
    idle_ttl: 5m              # 静默判定阈值（供 L2 次路径调度）

  longterm:                   # L2
    enabled: true
    router:
      enabled: true           # query-aware 路由；关闭则默认 fact+policy
    fact:
      enabled: true
      top_k: 5
      min_score: 0.5
      relevance: { exact: 1.0, partial: 0.5 }
    policy:
      enabled: true
    vector:                   # 默认关，依赖 Milvus
      enabled: false
      embedding_dim: 1536
      top_k: 5
      min_similarity: 0.7
    scoring: { confidence: 0.3, freshness: 0.2, relevance: 0.5 }
    budget_ratio: 0.2
    # admission control
    admission:
      write_threshold: 0.6
      max_entries_per_user: 1000
      max_extractions_per_session: 1   # LLM 次路径限流
    extraction_model: ""      # 空则复用默认 LLM

  evolution:
    enabled: true
    decay_factor: 0.95
    idle_scan_cron: "*/1 * * * *"   # 后端无关静默扫描
    cleanup_cron: "0 3 * * *"
```

---

## 五、分期实施路线

### Phase 1（MVP）— L0 增强 + L1 状态机 + L2 事实通道（纯确定性提取）
- [ ] `Message` 加 `ID`/`Timestamp`/`Metadata`；`sessions` 加 `last_active_at`/`summarized`
- [ ] `TaskState` 状态机 + `TaskStore`（内存/Redis）+ 载入/回写/循环内注入
- [ ] `FactStore`（MySQL）+ `memory_entries`（`channel`/`evidence`/`cardinality`/`value_hash`/`status`/`hit_count`，`agent_id NOT NULL DEFAULT ''`）
- [ ] **确定性提取主路径**（tool args / 正则 / 显式命令），零 LLM
- [ ] admission control：门禁 + 去重 + 配额（限流待次路径）
- [ ] 读取：`QueryRouter`（规则）+ Fact 通道 scoring + top-k + budget + `MarkUsedAsync`
- [ ] `injectEnrichment` 固定顺序追加，不替换 Base Prompt
- [ ] 后端无关静默扫描（cron 扫 `sessions`）预留触发点
- [ ] `memory_tasks` 待处理表 + 有限重试补偿

### Phase 2 — L2 LLM 次路径 + Policy 通道
- [ ] 静默触发全量摘要（LLM，每会话≤1 次）+ 限流
- [ ] `PolicyStore`：工具/行为偏好写入、`Match` 读取注入「行为偏好」
- [ ] 单值/多值键更正归档完善

### Phase 3 — L2 向量通道 + 演化引擎
- [ ] `EmbeddingProvider` + `VectorStore`（Milvus），首轮跳过、合并计时
- [ ] query 路由接入语义型 → Vector；与 Fact 合并统一评分
- [ ] `Evolve`：衰减/遗忘/归档清理 + cron；记忆健康度指标（条目数/命中率/均置信度）

---

## 六、关键设计原则

| 原则 | 说明 |
|------|------|
| **L0 不变** | 现有 `SessionStore` 完全保留，只增不改 |
| **热路径零 LLM** | 每轮都跑的 L1 更新、L2 提取主路径、query 路由一律确定性；LLM 仅静默摘要每会话≤1 次 |
| **通道分明** | L2 显式拆事实/策略/向量三通道，独立读写逻辑，杜绝黑盒 |
| **写入有门禁** | admission control：evidence+confidence 门禁、去重、配额、LLM 限流 |
| **读取按 query 路由** | 不同 query 走不同通道，再通道内 scoring+top-k+budget，防污染 |
| **读取不阻塞** | L2 各通道超时降级为无增强，不影响核心对话 |
| **DI 注入 / 用户粒度** | `MemoryManager` 构造注入 Engine；所有记忆按 `user_id` 隔离 |

---

## 七、工程落地关键决策

**7.1 异步写入一致性与补偿**：异步层失败只记 error（复用 `tracing.ErrFields`），绝不回滚 L0（唯一事实源可重放）；指数退避重试 3 次；仍失败落 `memory_tasks`（`pending/done/failed`），静默全量摘要重扫补齐，无需独立死信队列；写入以 `(user_id, agent_id, key_name, value_hash)` 唯一键 Upsert 幂等。

**7.2 用户标识统一 `user_id (int64)`**：JWT 有 `user_id` 与 `user_uuid`，记忆系统一律用 `user_id`，与 `SessionStore`/`ModelConfigStore`/`agentStore` 一致；`user_uuid` 仅对外/日志脱敏。

**7.3 多实例下的 L1**：`TaskState` 进程内内存在多副本下同 session 可能落不同实例导致丢失。单实例用内存；**多实例把 `task.backend` 设 redis**（key=`task:{sessionID}` 存 Hash，TTL 跟随 session），每轮载入/回写。

**7.4 删除级联与隐私**：
| 触发 | 动作 |
|------|------|
| 删 session | 删该 session 派生向量（`DeleteBySession`）+ `session_summaries` + `task:{sessionID}`；跨会话 `memory_entries` 默认保留 |
| 删用户 | 按 `user_id` 物删全部 L2 记忆与向量 |
| 「忘掉这个」 | 定位 key 物删或标 `archived` |

删 session 不连带删跨会话画像；用户级删除才全量物理清理。前端「删除会话」需明确此语义。
