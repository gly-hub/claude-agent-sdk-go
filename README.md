# Claude Agent SDK for Go

一个面向 Go 的 Claude Agent SDK，目标是尽量与 Python 版行为保持一致，同时保留 Go 常见的显式 API 风格。

## 已实现能力

- `Query` 单次请求
- `Client` 多轮交互式会话
- `stream-json` 子进程传输
- 控制协议：`initialize`、`interrupt`、`set_permission_mode`、`set_model`
- hooks / `CanUseTool`
- resume / continue / session store / session mirror
- SDK 内置 MCP server，支持 `tools/list` / `tools/call`
- plugins：当前支持 local plugin
- skills、thinking、effort、sandbox、output format 等 Python 对齐选项

## 示例

- [examples/quickstart/main.go](/Users/mac/my-project/claude-agent-sdk-go/examples/quickstart/main.go)：最小查询示例
- [examples/chat/main.go](/Users/mac/my-project/claude-agent-sdk-go/examples/chat/main.go)：参考 `chat.py` 的多轮聊天示例
- [examples/hooks/main.go](/Users/mac/my-project/claude-agent-sdk-go/examples/hooks/main.go)：hooks 与工具拦截
- [examples/mcp_server/main.go](/Users/mac/my-project/claude-agent-sdk-go/examples/mcp_server/main.go)：进程内 MCP 工具示例
- [examples/otel/main.go](/Users/mac/my-project/claude-agent-sdk-go/examples/otel/main.go)：OTEL trace context 与本地 stdout tracing 示例

## 安装

```go
module github.com/gly-hub/claude-agent-sdk-go
```

## Claude CLI

SDK 默认按下面顺序查找 Claude Code CLI：

1. `Options.CLIPath`
2. 当前可执行文件旁边的 `_bundled/claude`
3. `PATH` 里的 `claude`
4. 常见安装路径，例如 `~/.local/bin/claude`、`~/.claude/local/claude`

如果不希望主机预装 Claude Code，也不想把 CLI 打进 Go 包，可以显式开启按需下载：

```go
client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	AllowCLIDownload: true,
	CLIVersion:       "2.1.179", // 可选；为空时下载 latest
})
```

开启后，只有在本机找不到 Claude CLI 时才会从 `https://claude.ai/install.sh` 下载安装脚本，并把 CLI 缓存在用户 cache 目录。可用 `CLICacheDir` 指定缓存目录，或用 `CLIDownloadURL` 指向内部镜像。

## 快速开始

```go
package main

import (
	"context"
	"fmt"
	"log"

	claudeagentsdk "github.com/gly-hub/claude-agent-sdk-go"
)

func main() {
	ctx := context.Background()

	stream, err := claudeagentsdk.Query(ctx, "What is 2 + 2?", &claudeagentsdk.Options{
		SystemPrompt: "You are a concise assistant.",
		MaxTurns:     1,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	for msg := range stream.ReceiveResponseStream(ctx) {
		if assistant, ok := msg.(*claudeagentsdk.AssistantMessage); ok {
			for _, block := range assistant.Content {
				if text, ok := block.(claudeagentsdk.TextBlock); ok {
					fmt.Println(text.Text)
				}
			}
		}
	}
}
```

## 交互式 Client

`ReceiveResponseStream()` 更接近 Python 的 `async for msg in client.receive_response()`。

```go
client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	SystemPrompt: "You are a helpful coding assistant.",
})

if err := client.Connect(ctx); err != nil {
	log.Fatal(err)
}
defer client.Close()

if err := client.SendUser(ctx, "Review this idea and ask one follow-up question.", "default"); err != nil {
	log.Fatal(err)
}

for msg := range client.ReceiveResponseStream(ctx) {
	_ = msg
}
```

如果你需要一次性收集，也可以继续使用：

```go
messages, err := client.ReceiveResponse(ctx)
```

连接完成后可从初始化结果里拿到服务端信息：

```go
info := client.GetServerInfo()
sessionID, _ := info["session_id"].(string)
```

## Python 对齐选项

### `Skills`

`Skills` 对齐 Python：

- `nil`：不做 SDK 级自动配置
- `"all"`：允许 `Skill` 并默认补 `setting_sources=["user","project"]`
- `[]string{"pdf", "docx"}`：允许指定 skill，并在 initialize 里下发 skill 过滤
- `[]string{}`：显式发送空 skill 列表，抑制 skill listing

```go
client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	Skills: "all",
})
```

### `Plugins`

当前与 Python 一样，SDK 仅支持 local plugin，并映射到 `--plugin-dir`：

```go
client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	Plugins: []claudeagentsdk.SDKPluginConfig{
		{
			Type: claudeagentsdk.PluginTypeLocal,
			Path: "/absolute/path/to/plugin-or-.claude",
		},
	},
})
```

### `ExtraArgs`

Go 版使用显式结构区分“带值参数”和“布尔 flag”：

```go
client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	ExtraArgs: map[string]claudeagentsdk.ExtraArgValue{
		"agent":                  {Value: "reviewer"},
		"disable-slash-commands": {IsFlag: true},
	},
})
```

### 其他常用选项

```go
client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	User: "alice",
	Sandbox: claudeagentsdk.SandboxSettings{
		"enabled": true,
	},
	OutputFormat: &claudeagentsdk.OutputFormat{
		Type: "json_schema",
		Schema: map[string]any{
			"type": "object",
		},
	},
	TaskBudget:              &claudeagentsdk.TaskBudget{Total: 20_000},
	EnableFileCheckpointing: true,
	IncludePartialMessages:  true,
	IncludeHookEvents:       true,
	MaxBufferSize:           2 << 20,
})
```

说明：

- `User` 在 Darwin / Linux 下会尝试以对应系统用户启动 CLI 子进程
- `IncludePartialMessages=true` 时，会像 Python 一样注入 `CLAUDE_CODE_ENABLE_FINE_GRAINED_TOOL_STREAMING=1`
- `Connect(ctx)` / `Query(ctx, ...)` 会从传入的 `context.Context` 里尽力注入 OTEL trace context 到 CLI 环境变量
- `CLAUDE_CODE_ENTRYPOINT` 默认是 `sdk-go`，但仍可被 `Options.Env` 覆盖
- `CLAUDE_AGENT_SDK_VERSION` 始终由 SDK 注入

## Hooks

```go
options := claudeagentsdk.Options{
	AllowedTools: []string{"Bash"},
	Hooks: map[string][]claudeagentsdk.HookMatcher{
		"PreToolUse": {
			{
				Matcher: "Bash",
				Hooks: []claudeagentsdk.HookCallback{
					func(input map[string]any, toolUseID string, ctx claudeagentsdk.HookContext) (claudeagentsdk.HookOutput, error) {
						return claudeagentsdk.HookOutput{
							"hookSpecificOutput": map[string]any{
								"hookEventName":            "PreToolUse",
								"permissionDecision":       "deny",
								"permissionDecisionReason": "blocked by policy",
							},
						}, nil
					},
				},
			},
		},
	},
}
```

## OTEL Trace Context

如果你的应用已经在用 OpenTelemetry，可以直接把带 span 的 `ctx` 传给 `Connect(ctx)` 或 `Query(ctx, ...)`。SDK 会自动把当前 trace context 注入到 Claude CLI 子进程里。

```go
package main

import (
	"context"
	"log"

	claudeagentsdk "github.com/gly-hub/claude-agent-sdk-go"
	"go.opentelemetry.io/otel"
)

func main() {
	ctx := context.Background()
	tracer := otel.Tracer("my-app/claude")

	ctx, span := tracer.Start(ctx, "ask-claude")
	defer span.End()

	stream, err := claudeagentsdk.Query(ctx, "Summarize this repository in 3 bullets.", &claudeagentsdk.Options{
		SystemPrompt: "You are a concise assistant.",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	for range stream.ReceiveResponseStream(ctx) {
	}
}
```

效果是：

- 你的应用 span 和 Claude CLI 产生的 spans 会落到同一条 trace 里
- 如果环境里本来有旧的 `TRACEPARENT` / `TRACESTATE`，SDK 会在有活跃 span 时刷新为当前上下文
- 如果你显式在 `Options.Env` 里传了 `TRACEPARENT` 或 `TRACESTATE`，仍然以你的配置为准

如果你只是想在本地快速验证，可以用一个 stdout exporter 先把 trace 打到控制台：

```go
package main

import (
	"context"
	"log"

	claudeagentsdk "github.com/gly-hub/claude-agent-sdk-go"
	"go.opentelemetry.io/otel"
	stdouttrace "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func main() {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatal(err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("claude-agent-sdk-go-example"),
		)),
	)
	defer func() {
		_ = tp.Shutdown(context.Background())
	}()

	otel.SetTracerProvider(tp)

	ctx := context.Background()
	tracer := otel.Tracer("example/claude")
	ctx, span := tracer.Start(ctx, "interactive-query")
	defer span.End()

	stream, err := claudeagentsdk.Query(ctx, "Say hello in one sentence.", &claudeagentsdk.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	for range stream.ReceiveResponseStream(ctx) {
	}
}
```

跑完后你能先在本地看到应用侧 span 输出；如果 Claude CLI 也接入了同一套 tracing 后端，它产生的 spans 会挂在同一条 trace 下。

## SDK MCP Server

```go
calculator := claudeagentsdk.CreateSDKMCPServer("calculator", "1.0.0", []claudeagentsdk.MCPTool{
	claudeagentsdk.NewMCPTool(
		"add",
		"Add two numbers",
		map[string]any{"a": 1.0, "b": 1.0},
		func(args map[string]any) (claudeagentsdk.MCPToolResult, error) {
			a, _ := args["a"].(float64)
			b, _ := args["b"].(float64)
			return claudeagentsdk.MCPToolResult{
				Content: []claudeagentsdk.MCPToolContent{
					{Type: "text", Text: fmt.Sprintf("%v + %v = %v", a, b, a+b)},
				},
			}, nil
		},
	),
})

client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	MCPServers: map[string]claudeagentsdk.MCPServerConfig{
		"calc": calculator,
	},
	Tools:           []string{},
	AllowedTools:    []string{"mcp__calc__add"},
	StrictMCPConfig: true,
})
```

## 当前仍与 Python 不同的地方

- Python 的 `receive_response()` 是原生 async iterator；Go 侧提供了对等语义的 `ReceiveResponseStream()`，同时保留了收集型 `ReceiveResponse()`
- Python 使用 async context manager；Go 使用显式 `Connect()` / `Close()`
