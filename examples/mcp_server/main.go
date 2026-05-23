package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	claudeagentsdk "github.com/gly-hub/claude-agent-sdk-go"
)

func main() {
	ctx := context.Background()

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
		SystemPrompt: "For any arithmetic operation, you must use the available MCP tools and must not compute the answer mentally.",
		MCPServers: map[string]claudeagentsdk.MCPServerConfig{
			"calc": calculator,
		},
		Tools:           []string{},
		AllowedTools:    []string{"mcp__calc__add"},
		StrictMCPConfig: true,
		Stderr: func(line string) {
			fmt.Printf("CLI stderr: %s\n", line)
		},
	})

	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	status, err := client.GetMCPStatus(ctx)
	if err != nil {
		log.Fatal(err)
	}
	pretty, _ := json.MarshalIndent(status, "", "  ")
	fmt.Printf("MCP status:\n%s\n", pretty)

	if err := client.SendUser(ctx, "Use the calculator to add 2 and 3.", "default"); err != nil {
		log.Fatal(err)
	}

	for {
		msg, err := client.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		switch m := msg.(type) {
		case *claudeagentsdk.SystemMessage:
			if m.Subtype == "init" {
				pretty, _ := json.MarshalIndent(m.Data, "", "  ")
				fmt.Printf("Init message:\n%s\n", pretty)
			}
		case *claudeagentsdk.AssistantMessage:
			for _, block := range m.Content {
				if text, ok := block.(claudeagentsdk.TextBlock); ok {
					fmt.Println(text.Text)
				}
			}
		}
	}
}
