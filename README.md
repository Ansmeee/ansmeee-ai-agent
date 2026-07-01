# ansmeee-ai-agent
一个基于 Gin + LangChainGo 的 AI Agent 基础框架项目。

# 项目信息
- 项目名称：ansmeee-ai-agent
- Go 版本：1.25

# 目录结构
```
ansmeee-ai-agent/
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── agent/
│   │   ├── engine.go           # Agent 引擎封装
│   │   └── callback.go         # Agent 回调处理
│   ├── handler/
│   │   ├── chat.go             # 对话处理器
│   │   ├── stream.go           # 流式处理器
│   │   └── tool.go             # 工具查询处理器
│   ├── tool/
│   │   ├── registry.go         # 工具注册中心
│   │   ├── calculator.go
│   │   ├── datetime.go
│   │   └── web_search.go
│   ├── memory/
│   │   ├── interface.go        # 记忆接口
│   │   ├── memory.go           # 内存实现
│   │   └── redis.go            # Redis 实现
│   ├── llm/
│   │   └── provider.go         # LLM 客户端封装
│   ├── router/
│   │   └── router.go           # 路由注册
│   ├── middleware/
│   │   ├── requestid.go
│   │   ├── logger.go
│   │   ├── recovery.go
│   │   └── cors.go
│   └── config/
│       └── config.go           # 配置加载和结构体
├── pkg/
│   └── response/
│       └── response.go         # 统一响应封装
├── configs/
│   ├── config.yaml
│   └── config.example.yaml
├── .env.example
├── .gitignore
├── Makefile
├── Dockerfile
├── docker-compose.yaml
├── go.mod
└── README.md
```

# 核心依赖
- github.com/tmc/langchaingo # LangChain Go SDK
- github.com/gin-gonic/gin # HTTP 框架
- github.com/spf13/viper # 配置管理
- github.com/redis/go-redis/v9 # Redis 客户端（可选，支持内存降级）
- github.com/google/uuid # UUID 生成
- github.com/r3labs/sse/v2 # SSE 支持
- go.uber.org/zap # 结构化日志


# 功能要求

## API 端点
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/chat | 非流式对话，返回完整响应 |
| POST | /api/v1/chat/completion | 流式对话，SSE 推送 |
| GET | /api/v1/chat/:sessionId | 获取会话历史消息 |
| DELETE | /api/v1/chat/:sessionId | 删除并清理会话 |
| GET | /api/v1/tools | 获取可用工具列表及描述 |
| GET | /api/v1/health | 健康检查 |

## 请求/响应格式
```json
// POST /api/v1/chat 请求
{
  "session_id": "uuid-string",      // 可选，不传则新建会话
  "message": "用户消息内容",
  "stream": false,
  "metadata": {}                    // 可选的元数据
}

// 统一成功响应
{
  "code": 0,
  "message": "success",
  "data": {
    "session_id": "uuid",
    "response": "助手回复",
    "tool_calls": [],               // 工具调用记录
    "usage": {
      "prompt_tokens": 100,
      "completion_tokens": 50
    }
  }
}

// 统一错误响应
{
  "code": 错误码,
  "message": "错误描述",
  "data": null
}
```

## Setup

```bash
go mod init ansmeee-ai-agent
go mod tidy
```

## Build

```bash
go build ./...
```

## Test

```bash
go test ./...
```
