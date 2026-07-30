package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	claudeagentsdk "github.com/gly-hub/claude-agent-sdk-go"
)

func main() {
	ctx := context.Background()

	client := claudeagentsdk.NewClient(claudeagentsdk.Options{
		CWD:            "/Users/mac/my-project/agenttest",
		PermissionMode: claudeagentsdk.PermissionModeBypassPermissions,
		Plugins: []claudeagentsdk.SDKPluginConfig{
			{
				Type: claudeagentsdk.PluginTypeLocal,
				Path: "/Users/mac/my-project/agenttest/fangao/.claude",
			},
		},
		SettingSources:         []string{"project"},
		Skills:                 "all",
		Model:                  "deepseek-v4-flash",
		IncludePartialMessages: true,
		Env: map[string]string{
			"ANTHROPIC_AUTH_TOKEN": os.Getenv("ANTHROPIC_AUTH_TOKEN"),
			"ANTHROPIC_BASE_URL":   os.Getenv("ANTHROPIC_BASE_URL"),
		},
	})

	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("==================================================")
	fmt.Println("  Claude Agent Interactive Chat")
	fmt.Println("==================================================")
	fmt.Println("Type a message to chat, or 'exit'/'quit' to stop.")

	for {
		fmt.Print("\nYou: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") || strings.EqualFold(input, "q") {
			break
		}

		if err := client.SendUser(ctx, input, "default"); err != nil {
			log.Fatal(err)
		}

		fmt.Print("\nClaude: ")
		for msg := range client.ReceiveResponseStream(ctx) {
			switch m := msg.(type) {
			case *claudeagentsdk.AssistantMessage:
				for _, block := range m.Content {
					switch b := block.(type) {
					case claudeagentsdk.TextBlock:
						fmt.Print(b.Text)
					case claudeagentsdk.ToolUseBlock:
						fmt.Printf("\n[tool: %s]\n", b.Name)
					case claudeagentsdk.ToolResultBlock:
						fmt.Print(" [tool-ok]")
					}
				}
			}
		}
		fmt.Print("\n")
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}
