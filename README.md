# Claude Agent SDK for Go

一个面向 Go 的 Claude Agent SDK 封装，参考了 Python 版本的核心能力，并按 Go 的使用习惯做了接口收敛。

## 当前状态

这是一个已经可以开发和集成的首版 SDK，重点完成了核心会话、hooks、session 管理、session store 和进程内 MCP server。

如果你准备把它正式发布，建议优先确认两件事：

- 把 [go.mod](/Users/mac/my-project/claude-agent-sdk-go/go.mod) 里的模块名改成真实仓库地址
- 按你的发布策略补 `LICENSE`、tag 和 CI

当前版本优先覆盖这些能力：

- 通过 Claude Code CLI 启动会话
- `Query` 一次性请求
- `Client` 交互式多轮会话
- `stream-json` 消息流解析
- 常用 CLI 选项映射
- 基础控制协议：`initialize`、`interrupt`、`set_permission_mode`、`set_model`
- 工具权限回调 `CanUseTool`
- hooks 回调分发与 `hook_callback` 控制协议
- 常用运行期控制：`rewind_files`、MCP 重连/开关、`stop_task`
- 本地 session 管理：列出、读取、重命名、打 tag、删除、fork
- 进程内 SDK MCP server：支持 `tools/list` / `tools/call`
- local plugins：通过 `--plugin-dir` 加载插件目录

## 目录

- [examples/quickstart/main.go](/Users/mac/my-project/claude-agent-sdk-go/examples/quickstart/main.go): 最小查询示例
- [examples/hooks/main.go](/Users/mac/my-project/claude-agent-sdk-go/examples/hooks/main.go): hooks 与工具拦截
- [examples/mcp_server/main.go](/Users/mac/my-project/claude-agent-sdk-go/examples/mcp_server/main.go): 进程内 MCP 工具示例
- [examples/chat/main.go](/Users/mac/my-project/claude-agent-sdk-go/examples/chat/main.go): 多轮交互式聊天示例
- [examples/gin_sse_sqlite_chat](/Users/mac/my-project/claude-agent-sdk-go/examples/gin_sse_sqlite_chat): `gin + SSE + SQLite + static 前端` 的完整多会话 Web 示例

## 安装

因为这个仓库现在还没有绑定最终远程地址，`go.mod` 先用了占位模块名：

```go
module github.com/gly-hub/claude-agent-sdk-go
```

准备发布时，建议改成你自己的真实模块路径。

## 快速开始

```go
package main

import (
	"context"
	"fmt"
	"io"
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

	for {
		msg, err := stream.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		switch m := msg.(type) {
		case *claudeagentsdk.AssistantMessage:
			for _, block := range m.Content {
				if text, ok := block.(claudeagentsdk.TextBlock); ok {
					fmt.Println(text.Text)
				}
			}
		}
	}
}
```

## Plugins

Go 版的 `Plugins` 对齐 Python SDK 0.2.85 的 `plugins` 配置。目前只支持 local plugin，会为每个配置生成一组 `--plugin-dir <path>` 参数。

```go
client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	Plugins: []claudeagentsdk.SDKPluginConfig{
		{
			Type: claudeagentsdk.PluginTypeLocal,
			Path: "/absolute/path/to/plugin",
		},
	},
})
```

## 高级选项

这些选项对齐 Python SDK 0.2.85 的 `ClaudeAgentOptions`，会映射到 Claude Code CLI 参数或环境变量。

```go
client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	Sandbox: claudeagentsdk.SandboxSettings{
		"enabled": true,
	},
	OutputFormat: &claudeagentsdk.OutputFormat{
		Type: "json_schema",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{"type": "string"},
			},
		},
	},
	TaskBudget:              &claudeagentsdk.TaskBudget{Total: 20_000},
	EnableFileCheckpointing: true,
	MaxBufferSize:           2 << 20,
})
```

## 交互式 Client

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

messages, err := client.ReceiveResponse(ctx)
if err != nil {
	log.Fatal(err)
}
_ = messages
```

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
						toolInput, _ := input["tool_input"].(map[string]any)
						command, _ := toolInput["command"].(string)
						if strings.Contains(command, "foo.sh") {
							return claudeagentsdk.HookOutput{
								"hookSpecificOutput": map[string]any{
									"hookEventName": "PreToolUse",
									"permissionDecision": "deny",
									"permissionDecisionReason": "blocked by policy",
								},
							}, nil
						}
						return claudeagentsdk.HookOutput{}, nil
					},
				},
			},
		},
	},
}
```

## Session 管理

```go
sessions, err := claudeagentsdk.ListSessions("/path/to/project")
if err != nil {
	log.Fatal(err)
}

messages, err := claudeagentsdk.GetSessionMessages(
	"550e8400-e29b-41d4-a716-446655440000",
	"/path/to/project",
	0,
	0,
)
if err != nil {
	log.Fatal(err)
}

_ = claudeagentsdk.RenameSession("550e8400-e29b-41d4-a716-446655440000", "My Session", "/path/to/project")
_ = claudeagentsdk.TagSession("550e8400-e29b-41d4-a716-446655440000", "experiment", "/path/to/project")
fork, _ := claudeagentsdk.ForkSession("550e8400-e29b-41d4-a716-446655440000", "/path/to/project", "", "")
_ = fork
```

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
					{Type: "text", Text: fmt.Sprintf("%v", a+b)},
				},
			}, nil
		},
	),
})

client := claudeagentsdk.NewClient(claudeagentsdk.Options{
	MCPServers: map[string]claudeagentsdk.MCPServerConfig{
		"calc": calculator,
	},
	Tools:        []string{},
	AllowedTools: []string{"mcp__calc__add"},
	StrictMCPConfig: true,
})
```

## 设计说明

- 对外 API 采用 Go 常见的 `context.Context`、阻塞式 `Next()` 和显式 `Close()`
- 默认 transport 为本地 `claude` CLI 子进程
- 重点对齐 Python SDK 0.2.85 的高频能力，并保留 Go 风格的显式类型与错误返回
- 结构上已经把 transport、控制协议、消息解析、权限回调拆开，后续继续扩展会比较顺
