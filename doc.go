// Package claudeagentsdk provides a Go SDK for Claude Agent / Claude Code.
//
// It includes:
// - one-shot query helpers
// - an interactive client for multi-turn conversations
// - hooks and tool-permission callbacks
// - local session management helpers
// - session store abstractions with resume materialization
// - in-process SDK MCP servers for custom tools
//
// The SDK is designed around standard Go patterns such as context.Context,
// explicit resource cleanup, blocking reads, and interface-based transport
// extensibility.
package claudeagentsdk
