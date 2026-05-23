package claudeagentsdk

import (
	"context"
	"testing"
)

func TestConvertHookOutputKeywordFields(t *testing.T) {
	got := convertHookOutput(HookOutput{
		"continue_": true,
		"async_":    false,
		"reason":    "ok",
	})

	if got["continue"] != true {
		t.Fatalf("expected continue field to be converted, got %#v", got)
	}
	if got["async"] != false {
		t.Fatalf("expected async field to be converted, got %#v", got)
	}
	if _, exists := got["continue_"]; exists {
		t.Fatalf("unexpected continue_ field left behind: %#v", got)
	}
}

func TestBuildHooksConfigRegistersCallbacks(t *testing.T) {
	client := NewClient(Options{
		Hooks: map[string][]HookMatcher{
			"PreToolUse": {
				{
					Matcher: "Bash",
					Timeout: 12.5,
					Hooks: []HookCallback{
						func(input map[string]any, toolUseID string, ctx HookContext) (HookOutput, error) {
							return HookOutput{"continue_": true}, nil
						},
					},
				},
			},
		},
	})

	got := client.buildHooksConfig()
	rawMatchers, ok := got["PreToolUse"].([]any)
	if !ok || len(rawMatchers) != 1 {
		t.Fatalf("unexpected hooks config: %#v", got)
	}
	matcher, ok := rawMatchers[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected matcher config type: %#v", rawMatchers[0])
	}
	if matcher["matcher"] != "Bash" {
		t.Fatalf("unexpected matcher config: %#v", matcher)
	}
	if matcher["timeout"] != 12.5 {
		t.Fatalf("unexpected timeout config: %#v", matcher)
	}
	ids, ok := matcher["hookCallbackIds"].([]string)
	if !ok || len(ids) != 1 {
		t.Fatalf("unexpected callback IDs: %#v", matcher["hookCallbackIds"])
	}
	if client.hookCallbacks[ids[0]] == nil {
		t.Fatalf("callback was not registered: %#v", client.hookCallbacks)
	}
}

func TestHandleHookCallback(t *testing.T) {
	client := NewClient(Options{})
	id := client.registerHookCallback(func(input map[string]any, toolUseID string, ctx HookContext) (HookOutput, error) {
		if toolUseID != "tool-1" {
			t.Fatalf("unexpected toolUseID: %s", toolUseID)
		}
		return HookOutput{"continue_": false, "stopReason": "blocked"}, nil
	})

	resp, err := client.handleHookCallback(map[string]any{
		"callback_id": id,
		"tool_use_id": "tool-1",
		"input":       map[string]any{"tool_name": "Bash"},
		"subtype":     "hook_callback",
	})
	if err != nil {
		t.Fatalf("handleHookCallback() error = %v", err)
	}
	if resp["continue"] != false {
		t.Fatalf("expected converted continue field, got %#v", resp)
	}
	if resp["stopReason"] != "blocked" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestGetServerInfoReturnsCopy(t *testing.T) {
	client := NewClient(Options{})
	client.serverInfo = map[string]any{"output_style": "default"}

	got := client.GetServerInfo()
	got["output_style"] = "mutated"

	if client.serverInfo["output_style"] != "default" {
		t.Fatalf("server info was mutated: %#v", client.serverInfo)
	}
}

func TestReceiveResponseStopsAtResult(t *testing.T) {
	client := NewClient(Options{})
	client.msgCh <- &AssistantMessage{Content: []ContentBlock{TextBlock{Text: "hi"}}}
	client.msgCh <- &ResultMessage{Subtype: "success"}

	messages, err := client.ReceiveResponse(context.Background())
	if err != nil {
		t.Fatalf("ReceiveResponse() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("unexpected message count: %d", len(messages))
	}
	if _, ok := messages[1].(*ResultMessage); !ok {
		t.Fatalf("expected ResultMessage, got %T", messages[1])
	}
}
