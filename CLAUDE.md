# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands
- `make build` — Build the server to bin/server
- `make run` — Run the server with default config (configs/config.yaml)
- `make dev` — Run the server with `--config=configs/config.yaml`
- `go build ./...` — Build all packages
- `go test ./...` — Run all tests
- `go test -v -run TestName ./...` — Run a specific test
- `go vet ./...` — Run vet checks
- `go fmt ./...` — Format all files (Makefile uses `go fmt` not `gofmt`)
- `make test-cover` — Run tests with coverage report (outputs coverage.html)
- `make lint` — Run golangci-lint
- `docker compose up` — Start app + Redis via Docker

## Project Architecture

AI Agent framework built with Gin + LangChainGo. All dependencies are passed via constructor injection — no global state.

### Request Flow

```
Request → Recovery → RequestID → Logger → CORS → [JWTAuth] → Handler → Engine → LLM Provider → SSE/JSON Response
```

Protected routes under `/api/v1/*` require Bearer token (JWT with HS256, 7-day expiry). Auth routes are public.

### Core Components

1. **Agent Engine** (`internal/agent/engine.go`) — Orchestrates LLM calls with streaming
   - `ProcessStream()` returns `<-chan string` for SSE; `Process()` for full response
   - `maxIter = 5` for tool-calling loop
   - Callback hooks (`internal/agent/callback.go`): OnLLMStart/End, OnToolStart/End with zap logging
   - `AgentStore` (`internal/agent/store.go`) — GORM-backed CRUD for agent metadata in MySQL

2. **LLM Provider** (`internal/llm/provider.go`) — LangChainGo wrapper
   - Wraps `langchaingo/llms.OpenAI` with streaming via `WithStreamingFunc`
   - `StreamChat()` returns two channels: `<-chan string` (content) and `<-chan error`
   - `WithOverride()` creates provider copies for per-session model config
   - `ModelConfigStore` — per-user LLM settings stored in MySQL

3. **Tool Registry** (`internal/tool/registry.go`) — Thread-safe `map[string]tools.Tool`
   - Implements `langchaingo/tools.Tool` interface: `Name()`, `Description()`, `Call(ctx, input)`
   - Built-in tools: Calculator (AST-based math), DateTime, WebSearch (stub), Weather
   - Tools registered at startup in `cmd/server/main.go`

4. **Memory System** (`internal/memory/`) — Pluggable `SessionStore` with fallback
   - Interface: `AddMessage()`, `History()`, `Exists()`, `Delete()`, `ListSessions()`, `SetAgent()`, `GetAgent()`, `Close()`
   - `InMemoryStore` — map with 5-min TTL cleanup goroutine, `maxMessages` trim
   - `RedisStore` — `LPUSH`/`LTRIM`/`EXPIRE`/`SCAN`
   - `MySQLStore` — GORM `chat_messages` + `sessions` tables, user-scoped
   - Fallback: config `memory.type` selects backend; Redis → InMemory if connection fails

5. **Handlers** (`internal/handler/`)
   - `chat.go` — History, Delete, CreateSession, ListSessions
   - `stream.go` — SSE streaming via `writeSSE()`, headers: `text/event-stream` + `X-Accel-Buffering: no`
   - `auth.go` — Register, Login (bcrypt + JWT), Me
   - `agent.go` — CRUD for agent configs, user-scoped
   - `model_config.go` — Per-user model override
   - `tool.go`, `health.go`

6. **Middleware** (`internal/middleware/`)
   - Stack: Recovery → RequestID → Logger → CORS → JWTAuth
   - JWT sets `user_id`, `user_uuid`, `user_email` in gin context
   - Trace context: `X-Trace-ID` header, `trace_id`/`span_id` in logs

7. **Configuration** (`internal/config/config.go`)
   - Viper + `.env` (via godotenv) + env var override: `llm.api_key` → `LLM_API_KEY`
   - Structs: `Server`, `LLM`, `MySQL`, `Redis`, `Memory`, `Milvus`
   - Master/Slave MySQL via `dbresolver` — reads to slave, writes to master
   - Slave defaults to master if not configured

8. **Response Package** (`pkg/response/`)
   - Envelope: `{ "code": 0, "message": "success", "data": {...} }`
   - Error codes: `1001` (bad request), `1002` (unauthorized), `1004` (not found), `5000` (internal)

9. **Frontend** (`web/`) — Static HTML served at `/`, `/chat`, `/agents`, `/user`, `/login`

### API Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/api/v1/auth/register` | No | Register (email + password) |
| POST | `/api/v1/auth/login` | No | Login, returns JWT |
| GET | `/api/v1/auth/me` | Yes | Current user info |
| POST | `/api/v1/chat/completion` | Yes | Streaming chat (SSE) |
| GET | `/api/v1/chat/:sessionId` | Yes | Get chat history |
| DELETE | `/api/v1/chat/:sessionId` | Yes | Delete session |
| POST | `/api/v1/sessions` | Yes | Create session |
| GET | `/api/v1/sessions` | Yes | List sessions |
| GET | `/api/v1/tools` | Yes | List available tools |
| GET | `/api/v1/health` | Yes | Health check |
| GET | `/api/v1/user/model` | Yes | Get user model config |
| POST | `/api/v1/user/model` | Yes | Save user model config |
| GET | `/api/v1/agents` | Yes | List agents |
| GET | `/api/v1/agents/:id` | Yes | Get agent |
| POST | `/api/v1/agents` | Yes | Create agent |
| PUT | `/api/v1/agents/:id` | Yes | Update agent |
| DELETE | `/api/v1/agents/:id` | Yes | Delete agent |

### Startup Wiring (`cmd/server/main.go`)

1. `config.Load(path)` → `Config` (flag defaults to `configs/config.yaml`)
2. `zap.New*()` → logger (development if `server.mode=debug`)
3. `gorm.Open(mysql)` + `dbresolver` → `gormDB`
4. `agent.NewAgentStoreWithDB(gormDB)` → `agentStore`
5. `initSessionStore()` → `sessionStore` (MySQL/Redis/InMemory based on `memory.type`)
6. `llm.New(&cfg.LLM)` → `llmProvider`
7. `tool.NewRegistry()` + `Register(Calculator, DateTime, WebSearch, Weather)` → `registry`
8. `agent.New(llmProvider, registry, sessionStore, callback)` → `engine`
9. `llm.NewModelConfigStore(gormDB)` → `modelConfigStore`
10. `router.Setup(...)` → `gin.Engine`
11. Graceful shutdown on SIGINT/SIGTERM

### Key Design Patterns

- **DI via constructors** — all deps passed explicitly
- **Memory fallback** — Redis→InMemory if Redis unavailable
- **Channel-based streaming** — `StreamChat()` returns `<-chan string` + `<-chan error`; handler iterates and writes SSE
- **Dynamic model override** — `ModelConfigStore` allows per-user LLM settings via `WithOverride()`
- **Master/slave DB** — `dbresolver` routes reads to slave, writes to master
