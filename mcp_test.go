package claudeagentsdk

import "testing"

func TestSDKMCPServerHandleListAndCall(t *testing.T) {
	serverConfig := CreateSDKMCPServer("calculator", "1.0.0", []MCPTool{
		NewMCPTool("add", "Add numbers", map[string]any{"a": 1.0, "b": 1.0}, func(args map[string]any) (MCPToolResult, error) {
			return MCPToolResult{
				Content: []MCPToolContent{{Type: "text", Text: "sum"}},
				IsError: false,
			}, nil
		}),
	})
	server := serverConfig.Instance

	listResp := server.HandleMessage(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	result, _ := listResp["result"].(map[string]any)
	tools, _ := result["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %#v", result["tools"])
	}

	callResp := server.HandleMessage(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "add",
			"arguments": map[string]any{"a": 1.0, "b": 2.0},
		},
	})
	callResult, _ := callResp["result"].(map[string]any)
	content, _ := callResult["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "text" {
		t.Fatalf("unexpected call result: %#v", callResp)
	}
}

func TestClientHandleMCPMessage(t *testing.T) {
	config := CreateSDKMCPServer("calculator", "1.0.0", []MCPTool{
		NewMCPTool("echo", "Echo input", map[string]any{"text": "string"}, func(args map[string]any) (MCPToolResult, error) {
			return MCPToolResult{
				Content: []MCPToolContent{{Type: "text", Text: stringFromAny(args["text"])}},
			}, nil
		}),
	})
	client := NewClient(Options{
		MCPServers: map[string]MCPServerConfig{
			"calc": config,
		},
	})

	resp, err := client.handleMCPMessage(map[string]any{
		"server_name": "calc",
		"message": map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "echo",
				"arguments": map[string]any{"text": "hello"},
			},
		},
	})
	if err != nil {
		t.Fatalf("handleMCPMessage() error = %v", err)
	}
	mcpResp, _ := resp["mcp_response"].(map[string]any)
	result, _ := mcpResp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("unexpected MCP response: %#v", resp)
	}
}
