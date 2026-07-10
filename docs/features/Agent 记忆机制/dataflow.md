# Agent 记忆机制 — 数据流转（Phase 1）

> 依据当前已落地代码：`engine.go` 挂点 + `MemoryManager` 编排 + L0/L1/L2 三层 store。
> 范围：Phase 1 MVP（确定性提取，热路径零 LLM）。

---

## 一、三条链路总览

```
              ┌─────────────────────────────────────────────┐
   用户消息 ─▶ │  ProcessStream (engine.go)  —— 编排入口       │
              └───┬───────────────┬──────────────────┬──────┘
                  │ 读取链路        │ 主对话(ReAct)     │ 写入链路(异步)
                  ▼               ▼                  ▼
             Retrieve         ChatStream×N       OnTurnEnd(go)
             (L1+L2 读)        (LLM+工具)         (确定性提取→L2 写)

              演化链路(后台独立): IdleScanner ──▶ OnIdle(Phase1 stub) / Evolve
```

---

## 二、单轮完整时序（memMgr ≠ nil）

```
① 进入 ProcessStream
   prompt = agentCfg.Prompt || systemPrompt

② task = memMgr.Load(ctx, sessionID)          ── L1 读:memTaskStore 取上一轮 TaskState(Clone)
   turnDelta = [ {human, userMessage} ]        ── 起本轮消息累加器

③ mem.AddMessage(human)                        ── L0 写:ai_chat_session_history 落库
   └─ 同事务:ai_chat_session.last_active_at=now, summarized=false  (idle-scan 活跃标记)

④ history = mem.History(sessionID)             ── L0 读:全量历史

⑤ enrich = memMgr.Retrieve(userID,"",sid,msg,task)   ★读取链路(见 §四)
   prompt = appendEnrichment(prompt, enrich)   ── 增强段拼到 system prompt 顶部
   messages = buildMessages(prompt, history)

⑥ ReAct 循环 (iter < maxIter):
   result = llm.ChatStream(messages, tools)
   ├─ 无 tool_calls ─▶ finalizeTask(...)  ★收尾(见 §三)→ return
   └─ 有 tool_calls:
        mem.AddMessage(function, toolCallJSON)      ── L0 写
        UpdateTaskState(task, ToolCallEvent)        ── L1:stage=executing, step=doing
        执行工具(串行/并行)
        mem.AddMessage(tool, toolResultJSON)        ── L0 写
        UpdateTaskState(task, ToolResultEvent)      ── L1:step=done/failed, slot=summary
        turnDelta += [function msg, tool msg...]

⑦ 兜底最终回复(maxIter 用尽)─▶ finalizeTask(...)
```

---

## 三、收尾 finalizeTask —— L1 定稿 + 触发写入

```
finalizeTask(ctx, task, turnDelta, sessionID, userID, content):
   UpdateTaskState(task, FinalReplyEvent(content))
      └─ scanPending(content):
           命中 "需确认:/TODO:" ─▶ stage=confirming, pending+=lines
           否则                 ─▶ stage=done
   mem.AddMessage(ai, content)                 ── L0 写:最终回复
   turnDelta += {ai, content}
   memMgr.Save(ctx, sessionID, task)           ── L1 写:回存 TaskState(Clone, TTL 刷新)
   go memMgr.OnTurnEnd(context.WithoutCancel(ctx), userID, sessionID, turnDelta)  ★异步写入
```

- `context.WithoutCancel`：请求 ctx 结束后异步提取仍存活。
- 三处 error return **不** Save（不持久化半成品）。

---

## 四、读取链路 Retrieve（热路径，限时，零 LLM）

```
Retrieve(userID, agentID="", sessionID, userMsg, task):
   ① longterm.enabled==false || fact==nil ─▶ 直接返回 renderTaskState(task) 或 ""  (短路)
   ② feats = router.Route(userMsg)              ── 确定性分类
        疑问词/记忆词/key提示命中 ─▶ Class=Fact, Keywords=[name/city/email...]
        否则                     ─▶ Class=Default(跳过 L2)
   ③ if Class==Fact:
        errgroup + context.WithTimeout(200ms):
          scored = fact.Recall(RecallQuery{userID, agentID, Keywords, TopK, MinScore})
             ├─ SQL: WHERE user_id&agent_id&channel=fact&status=active
             ├─ 内存评分: 0.3·confidence + 0.2·freshness + 0.5·relevance
             │     freshness = decay^days_since(COALESCE(last_used_at,created_at))
             │     relevance = 精确key命中1.0 / 部分0.5 / 0
             ├─ 过滤 MinScore, 排序取 TopK
             └─ MarkUsedAsync(ids) ── 异步回写 hit_count+1/last_used_at(不阻塞读)
        超时/出错 ─▶ 该通道跳过(降级,不阻断)
   ④ 拼接: renderTaskState(task)  "## 当前任务"
          + renderEnrichment(facts) "## 用户信息\nuser.name: 张三"
          按 budget_ratio 截断 ─▶ 返回字符串
```

数据落点：**L1（task，进程内）+ L2 fact（MySQL）** 并入一段文本 → system prompt。

---

## 五、写入链路 OnTurnEnd（异步主路径，确定性）

```
OnTurnEnd(ctx, userID, sessionID, turnDelta):    (defer recover 包裹,错误只记日志)
   entries = extractor.Extract(sessionID, turnDelta)   ── 零 LLM
      ├─ human 消息:
      │    正则  "我叫X/我在X/我的邮箱是X" ─▶ user.name/city/email (conf0.9, single, rule)
      │    "记住…"                        ─▶ user.note        (conf1.0, multi, user_stated)
      └─ function 消息(tool args JSON):
           weather(city=北京) ─▶ user.mentioned_city=北京   (conf0.9, multi, rule)
   for e in entries:
      e.UserID=userID; e.AgentID=""                  ── 打上归属
      fact.Admit(e, admissionConfig):                ── L2 写,四步
         ① 门禁: hasEvidence && confidence>=0.6      否则丢弃
         ② 查同 (user,agent,key) active 集 → decideAdmit:
              value_hash 相同 ─▶ skip(幂等)
              single 且已存在 ─▶ archive 旧 + insert
              multi           ─▶ 共存
         ③ enforceQuota: 超 max_entries_per_user ─▶ 删最低分行
         ④ OnConflict(uk_uakv) Upsert ─▶ memory_entries 落库
```

数据落点：**turnDelta → memory_entries（MySQL）**。写放大由 `MarkUsedAsync` 的 buffered channel + 批量 UPDATE 吸收。

---

## 六、演化链路（后台，独立于请求）

```
IdleScanner.Start(ctx)  ── ticker(idle_scan_cron)
   扫描 ai_chat_session WHERE last_active_at < now-idle_ttl AND summarized=false
   Phase 1: memMgr.OnIdle(...)  ── stub,仅记日志(不触发 LLM)
            [Phase 2 才接 summarizer → Admit,并置 summarized=1]

fact.Evolve(userID, now)  ── [Phase 1 最小实现]
   DELETE WHERE confidence<0.1 OR expires_at<now      ── 遗忘/过期硬删
```

---

## 七、各层数据落点一览

| 层 | 数据 | Store | 后端 | 生命周期 |
|----|------|-------|------|---------|
| L0 | 原始对话 | SessionStore | MySQL `ai_chat_session_history` | 持久 |
| L0 | 活跃标记 | — | MySQL `ai_chat_session.last_active_at/summarized` | 持久 |
| L1 | TaskState | memTaskStore | 进程内 map | TTL（默认 30m） |
| L2 | 事实 | FactStore | MySQL `memory_entries` | 持久 + Evolve 衰减 |
| L2 | 会话摘要 | — | MySQL `session_summaries` | Phase 2 启用 |

---

## 八、关键短路点（零回归保证）

```
memMgr == nil               ─▶ ②⑤⑥⑦ 全部跳过,ProcessStream == 主干(逐字节)
longterm.enabled == false   ─▶ Retrieve/OnTurnEnd 内层短路,仅保留 L1 task 文本
Class == Default            ─▶ 跳过 L2 Recall(非记忆类提问不查库)
fact == nil / 通道超时       ─▶ 该通道降级跳过,不阻断主对话
```

---

## 九、跨会话记忆示例（同一 user，session A→B）

```
session A: 用户「我叫张三」
   OnTurnEnd → extractor 正则命中 → Admit(user.name=张三, conf0.9)
            → memory_entries 落库 (user_id=U, agent_id='')

session B: 用户「我叫什么」
   Retrieve → router 命中 "叫什么"→Keywords=[name], Class=Fact
           → fact.Recall(user_id=U, keywords=[name]) → 命中 user.name=张三
           → enrich "## 用户信息\nuser.name: 张三" 注入 system prompt
   LLM 读到跨会话事实 → 回答「你叫张三」    (全程零新增 LLM 调用)
```
