# Agent 通用记忆机制 — Phase 1 落地计划（plan.md）

> 项目：ansmeee-ai-agent
> 依据：`design.md`（技术方案）· `proposal.md`（v3 需求）
> 范围：**仅 Phase 1 MVP** —— L0 增强 + L1 任务状态机 + L2 事实通道（确定性提取，零额外 LLM）。Policy/LLM 摘要/向量归 Phase 2、3。
> 本文回答「按什么顺序、改哪些文件、每步怎么验收」。

---

## 〇、施工前的两处真实代码校准（覆盖 design 措辞，以本文为准）

对照当前代码，design 有两处描述需修正，施工按下述执行：

| design 原文 | 真实情况 | 施工调整 |
|------------|---------|---------|
| §3.1/§11「`mysql.go` 的 sessions 模型加 2 列」 | session/消息的 GORM 模型在 **`internal/models`**（`models.Session`/`models.ChatMessage`），`mysql.go` 只是仓储逻辑 | `last_active_at`/`summarized` 两列加到 **`internal/models` 的 `Session` 结构**；`mysql.go` 只改 `AddMessage`（写 `last_active_at`、人类消息重置 `summarized=0`） |
| §3.1「`chat_messages` 加 `msg_id`」 | `models.ChatMessage` **已有 `UUID`**（`genMsgUUID()` 生成） | **不新增列**；`History()` 把 `r.UUID` 映射进 `Message.ID` 即可 |

其余 design 内容（接口签名、§5.2 行号挂点、`agent_id=''` 用户级、`memMgr==nil` 短路）均与真实代码一致，照做。

---

## 一、总原则（每一步都必须守住）

1. **每步可编译、可测试**：任一步结束 `go build ./...` 通过；涉及逻辑的步骤附单测且 `go test` 绿。
2. **未接入零影响**：`memMgr == nil` 时 `ProcessStream` 行为与现状逐字节一致——这是回滚兜底，也是集成测试的黄金基准。
3. **热路径零 LLM**：Phase 1 不引入任何新增 LLM 调用（`idle_scan.go` 只留骨架，摘要器 Phase 2 接）。
4. **纯增量优先**：能新增文件解决的不改老文件；改老文件时把改动收敛到 §5.2 指明的几个挂点。
5. **L0 只增不改**：`Message` 三个新字段皆 `omitempty` 可选；`SessionStore` 接口签名不动。

---

## 二、依赖顺序（DAG）

```
S1 数据模型+配置骨架 ─┐
                      ├─→ S6 MemoryManager ─→ S8 Engine 集成 ─→ S9 main 装配 ─→ S10 全量验证
S2 L0 增强 ───────────┤          ▲
S3 L1 TaskState ──────┤          │
S4 L2 Fact 通道 ──────┤          │
S5 Extractor+Router ──┘          │
S7 IdleScanner 骨架 ─────────────┘（可并入 S9 前任意时点）
```
- **S1/S2/S3/S4/S5 相互独立**，可并行开工。
- **S6** 依赖 S3+S4+S5（外加 S1 的配置类型）。
- **S8** 依赖 S6；**S9** 依赖 S8；**S10** 收尾。

---

## 三、分步施工清单

### S1 · 数据模型 + 配置骨架（纯新增，零行为变化）

**改动**
- `internal/config/config.go`：`MemoryConfig` 增 `Task/LongTerm/Evolution` 及子结构（design §六照抄）；`applyDefaults()` 末尾追加记忆默认值块（沿用现有 `if ==0/""` 风格）：
  - `task.backend=memory`、`idle_ttl=5m`
  - `longterm.enabled=true`、`router=true`、`budget_ratio=0.2`
  - `admission.write_threshold=0.6`、`max_entries_per_user=1000`、`max_extractions_per_session=1`
  - `scoring=0.3/0.2/0.5`、`fact.top_k=5/min_score=0.5`、`vector.enabled=false`
  - `evolution.decay_factor=0.95`、`idle_scan_cron="*/1 * * * *"`、`cleanup_cron="0 3 * * *"`
- `internal/memory/entry.go`（新）：`MemoryEntry`、`SessionSummary` GORM 模型 + `TableName()`（design §3.2 照抄）。

**验收**：`go build ./...`；新增 `config_test.go` 断言默认值；空 `memory:` 配置能加载且默认生效。

---

### S2 · L0 最小增强

**改动**
- `internal/memory/interface.go`：`Message` 增 `ID/Ts/Meta`（`json:omitempty`）。
- `internal/models` 的 `Session` 结构：增 `LastActiveAt *time.Time`、`Summarized bool`（`default:0`）。
- `internal/memory/mysql.go`：`AddMessage` 内——每条消息 `last_active_at=now`；`Role==RoleHuman` 时 `summarized=0`（在现有 backfill title 的同一 `Update` 里合并）。`History()` 把 `r.UUID`→`Message.ID`、`r.CreatedAt`→`Message.Ts`。
- `memory.go`(InMemory)/`redis.go`：新字段是附加值，确认编译通过即可，无需逻辑改动。

**验收**：`go build ./...`；现有 `./internal/memory/...` 测试全绿（回归 L0 未破坏）；`AddMessage` 后 `sessions.last_active_at` 被更新的单测（用 sqlite 或现有测试 DB 夹具）。

---

### S3 · L1 TaskState + TaskStore（内存）

**改动（新文件）**
- `taskstate.go`：`Step`、`TaskState`、`TaskEvent`（含 `FinalReplyEvent(content)` 构造）、`updateTaskState(ts, ev)` 规则状态机（proposal §二转移表）、`renderTaskState(ts)`（产出「## 当前任务」文本段）。
- `taskstore.go`：`TaskStore` 接口（`Load/Save/Delete`）+ `memTaskStore`（`sync.Map`+TTL）+ `NewTaskStore(&cfg.Memory, redisClient)` 工厂（Phase 1 只走 memory 分支；redis 分支留 `Phase 2` TODO）。

**验收**：`updateTaskState` 表驱动单测覆盖全部转移（首条消息→planning、tool_call→executing、成功/失败→step 状态、`FinalReplyEvent`→done、`需确认:`→pending）；`memTaskStore` load/save/delete/TTL 过期单测。

---

### S4 · L2 事实通道 FactStore

**改动（新文件）**
- `fact_store.go`：`RecallQuery`、`ScoredEntry`、`AdmissionConfig`（若未随 S1 放 config 侧则置此）、`FactStore` 接口 + `gormFactStore`：
  - `NewFactStore(db)`：内部 `AutoMigrate(&MemoryEntry{}, &SessionSummary{})`。
  - `Admit`：四步（门禁 evidence+confidence → 去重 uk_uakv 按 cardinality 合并 → 配额淘汰最低分 → `clause.OnConflict` Upsert）。
  - `Recall`：SQL 过滤 + 内存 `score=0.3c+0.2f+0.5r`、`freshness=decay^days_since(COALESCE(last_used_at,created_at))`、`relevance` 精确/部分/0，过滤 `MinScore` 取 `TopK`，返回后 `MarkUsedAsync(ids)`。
  - `Get`、`MarkUsedAsync`（buffered channel + 后台批量 `UPDATE` goroutine）、`Evolve`（Phase 1 留最小实现或 stub，Phase 3 补衰减）。

**验收**：`Admit` 四步分别单测（门禁拒绝、单值覆盖归档、多值 value_hash 并存、配额淘汰、Upsert 幂等）；`Recall` 评分排序 + TopK + MinScore 单测；`agent_id=''` 下 uk_uakv 去重生效单测。

---

### S5 · 确定性提取器 + QueryRouter

**改动（新文件）**
- `extractor.go`：`Rule`、`DeterministicExtractor` + `Extract(sessionID, delta)`：
  - tool args 解析（`RoleFunction`/`RoleTool` 消息里的参数 → `fact`，source=`rule`,conf=0.9）
  - 正则（`我叫X`/`我是X`/`我在X`/`我的邮箱是X` → `user.name/user.city/...`）
  - 显式命令（`记住…`→`user_stated`,1.0；`忘掉…`→删除/归档标记）
  - 产出统一交调用方过 `Admit`。
- `router.go`：`QueryClass`、`QueryFeatures`、`QueryRouter.Route(msg)`。Phase 1 只需 `ClassFact`/`ClassDefault` 两类起步（疑问词/指代/记忆词命中→Fact，否则 default）。

**验收**：Extractor 各来源 → 期望 `MemoryEntry` 表驱动单测 + 「零 LLM」断言（不依赖 llm 包）；Router 中文样例分类单测。

---

### S6 · MemoryManager 编排（fact-only）

**改动（新文件）**
- `manager.go`：`MemoryManager` 结构（design §4.8）+ `NewMemoryManager(...)`：
  - `Load`/`Save`：转发 `task.Load/Save`（`memMgr` 为 `nil` 或 task 为 nil 时 no-op）。
  - `Retrieve`：`router.Route` → `errgroup`+`context.WithTimeout`(fact 200ms) 调 `fact.Recall` → 评分已在 Recall 内 → 按 `budget_ratio` 截断 → `renderEnrichment` 拼「## 用户信息」等段并返回字符串。Phase 1 policy/vector 通道为 nil 直接跳过。
  - `OnTurnEnd`：`extract.Extract(delta)` → 逐条 `fact.Admit`；panic recover 包裹；错误只记日志。
  - `OnIdle`：Phase 1 留 stub（记日志），Phase 2 接摘要器。
- `appendEnrichment(prompt, enrich)` 工具函数（放 manager 或 engine 皆可；design §5.2 语义：空则原样返回）。

**验收**：`Retrieve` 超时降级（mock 慢 fact）、budget 截断、`longterm.enabled=false`/`fact=nil` 短路单测；`OnTurnEnd` 提取→Admit 落库集成单测。

---

### S7 · IdleScanner 骨架

**改动（新文件）**
- `idle_scan.go`：`IdleScanner` + `NewIdleScanner(gormDB, memMgr, &cfg.Memory)` + `Start(ctx)`：cron 扫 `sessions WHERE last_active_at < now-idle_ttl AND summarized=false`，Phase 1 命中仅调 `memMgr.OnIdle`（stub）并置 `summarized=1` 的逻辑可先留 TODO，**保证不触发任何 LLM**。

**验收**：`go build`；扫描 SQL 条件单测（可选，用测试 DB）；确认 Phase 1 不产生 LLM 调用。

---

### S8 · Engine 集成（design §5.2 精确挂点）

**改动**：`internal/agent/engine.go`
- 结构加 `memMgr *memory.MemoryManager` + `WithMemoryManager` option（`nil` 短路）。
- `ProcessStream` 内按 design §5.2：
  - L136 前：`Load(task)` + 起 `turnDelta`，append 用户消息。
  - L146 前：`const memAgentID=""` → `Retrieve(...)` → `appendEnrichment(prompt, enrich)`。
  - L192-200 工具分支后：`UpdateTaskState(task, toolEvent)` + `turnDelta = append(..., toolMsgs...)`。
  - **两处 done 分支**（L177/L181 无工具直答、L218/L222 兜底最终回复）前：`UpdateTaskState(task, FinalReplyEvent(result.Content))` + append AI 消息 + `Save(task)` + `go OnTurnEnd(context.WithoutCancel(ctx), ...)`。
- 三处 error return（L139/L170/L213）**不回写 task**（出错不持久化半成品，符合 design §八）。

**验收**：集成测试（mock LLM）——(a) 增强段确注入进 system message；(b) 跨轮 task 保持（第二轮读到上一轮 goal/stage）；(c) **`memMgr=nil` 行为与改动前逐字节一致**；(d) 无工具直答路径 task 置 `done`。`go test ./internal/agent/...` 绿。

---

### S9 · main 装配 + 配置样例

**改动**
- `cmd/server/main.go`（design §7，L95 `agent.New` 之前）：`if cfg.Memory.LongTerm.Enabled` → 构造 `NewFactStore(gormDB)`、`NewTaskStore(&cfg.Memory, nil)`（Phase 1 backend=memory 传 nil）、`NewMemoryManager(...)`、`go NewIdleScanner(...).Start(ctx)`；`agent.New(...)` 追加 `agent.WithMemoryManager(memMgr)`（memMgr 可 nil）。
  - **redis client 作用域（design §7 F5）**：Phase 1 `task.backend=memory` 传 `nil`；若后续启用 redis task 后端，需先把 redis client 从 `initSessionStore` 提升为 `main` 顶层变量。
- `configs/config.yaml`：追加 design §六的 `memory.task/longterm/evolution` 配置块（默认值与 `applyDefaults` 一致，便于查看）。

**验收**：`go build ./...`；`go vet ./...`；`make build` 产出 `bin/server`；本地起服务冒烟（`enabled=true` 与 `enabled=false` 两种配置都能正常启动）。

---

### S10 · 全量验证

- `go test ./internal/memory/... ./internal/agent/...` 全绿。
- `go vet ./...`、`go fmt ./...`、`make lint`（若可用）。
- **端到端冒烟**：同一 user 两个不同 session——session A 说「我叫张三/我在北京」，session B 提问「我叫什么/我在哪」，验证 system prompt 注入了跨会话事实且**全程无新增 LLM 调用**（日志核对）。
- 基准对照：`enabled=false` 时 `/api/v1/chat/completion` 输出与主干一致。

---

## 四、验收总表（Definition of Done）

| 项 | 判据 |
|----|------|
| 编译 | `go build ./...` 通过 |
| 单测 | `go test ./internal/memory/... ./internal/agent/...` 全绿 |
| 静态 | `go vet ./...` 无告警；`go fmt` 无 diff |
| 零回归 | `memMgr=nil`/`enabled=false` 行为同主干 |
| 零额外 LLM | Phase 1 端到端日志无新增 LLM 调用 |
| 跨会话记忆 | 冒烟场景：B 会话读到 A 会话确定性事实 |
| 迁移安全 | `AutoMigrate` 幂等；`agent_id=''` 下 uk_uakv 去重生效 |

---

## 五、风险与回滚

| 风险 | 缓解 |
|------|------|
| 集成改动影响主对话 | `memMgr=nil` 全短路；集成测试以主干为黄金基准；配置 `enabled=false` 一键关停 |
| 异步提取 goroutine 泄漏/panic | `context.WithoutCancel` + `recover` 包裹；错误只记日志不阻塞 SSE |
| `AutoMigrate` 在生产表加列 | 新表 `memory_entries`/`session_summaries` 纯新增；`Session` 加列为可空/带默认，向后兼容 |
| `MarkUsedAsync` 写放大 | buffered channel + 批量 UPDATE；channel 满时丢弃（读路径不受影响） |
| 回滚 | 配置 `longterm.enabled=false` 即回到现状；代码层删 `WithMemoryManager` 一行 |

---

## 六、超出 Phase 1（不在本计划）

- **Phase 2**：`summarizer.go` LLM 摘要次路径（`OnIdle` 接入、`max_extractions_per_session` 限流）、`policy_store.go` 策略通道、单值/多值归档完善、redis task 后端 + 顶层 redis client 提升。
- **Phase 3**：`vector_store.go`+`embedding.go`（Milvus，复用顶层 `cfg.Milvus`）、语义路由接 Vector、`Evolve` 衰减/遗忘 cron、记忆健康度指标。

---

## 七、建议提交切分（每个 = 一个可独立 review 的 commit）

1. `feat(memory): config + GORM 模型骨架`（S1）
2. `feat(memory): L0 Message/Session 增强`（S2）
3. `feat(memory): L1 TaskState 状态机 + 内存 store`（S3）
4. `feat(memory): L2 FactStore admission + recall`（S4）
5. `feat(memory): 确定性提取器 + QueryRouter`（S5）
6. `feat(memory): MemoryManager 编排（fact-only）`（S6）
7. `feat(memory): IdleScanner 骨架`（S7）
8. `feat(agent): Engine 接入 MemoryManager`（S8）
9. `feat(server): 装配记忆管理器 + 配置样例`（S9）
10. `test(memory): 集成与端到端验证`（S10）

**起手点**：S1 与 S2 可立即并行开工，二者均为纯增量、风险最低。
