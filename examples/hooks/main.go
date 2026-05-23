package main

import (
	"context"
	"io"
	"log"
	"strings"

	claudeagentsdk "github.com/gly-hub/claude-agent-sdk-go"
)

func main() {
	ctx := context.Background()

	client := claudeagentsdk.NewClient(claudeagentsdk.Options{
		AllowedTools: []string{"Bash"},
		Hooks: map[string][]claudeagentsdk.HookMatcher{
			"PreToolUse": {
				{
					Matcher: "Bash",
					Hooks: []claudeagentsdk.HookCallback{
						func(input map[string]any, toolUseID string, ctx claudeagentsdk.HookContext) (claudeagentsdk.HookOutput, error) {
							toolInput, _ := input["tool_input"].(map[string]any)
							command, _ := toolInput["command"].(string)
							if strings.Contains(command, "rm -rf") {
								return claudeagentsdk.HookOutput{
									"hookSpecificOutput": map[string]any{
										"hookEventName":            "PreToolUse",
										"permissionDecision":       "deny",
										"permissionDecisionReason": "dangerous command blocked",
									},
								}, nil
							}
							return claudeagentsdk.HookOutput{}, nil
						},
					},
				},
			},
		},
	})

	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	if err := client.SendUser(ctx, "Run: echo hello", "default"); err != nil {
		log.Fatal(err)
	}
	if err := client.EndInput(); err != nil {
		log.Fatal(err)
	}

	for {
		_, err := client.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
	}
}
