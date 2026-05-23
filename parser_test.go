package claudeagentsdk

import "testing"

func TestParseAssistantMessage(t *testing.T) {
	raw := map[string]any{
		"type":       "assistant",
		"session_id": "sess-1",
		"uuid":       "msg-1",
		"message": map[string]any{
			"model": "claude-sonnet",
			"content": []any{
				map[string]any{"type": "text", "text": "hello"},
				map[string]any{"type": "tool_use", "id": "tool-1", "name": "Read", "input": map[string]any{"file_path": "a.txt"}},
			},
		},
	}

	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}

	assistant, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("expected AssistantMessage, got %T", msg)
	}
	if assistant.Model != "claude-sonnet" {
		t.Fatalf("unexpected model: %s", assistant.Model)
	}
	if len(assistant.Content) != 2 {
		t.Fatalf("unexpected block count: %d", len(assistant.Content))
	}
}

func TestParseResultMessageExtendedFields(t *testing.T) {
	raw := map[string]any{
		"type":               "result",
		"subtype":            "success",
		"session_id":         "sess-1",
		"uuid":               "uuid-1",
		"model_usage":        map[string]any{"claude": float64(1)},
		"permission_denials": []any{map[string]any{"tool": "Bash"}},
		"deferred_tool_use":  map[string]any{"id": "tool-1"},
		"errors":             []any{"oops"},
		"api_error_status":   float64(429),
	}

	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	result, ok := msg.(*ResultMessage)
	if !ok {
		t.Fatalf("expected ResultMessage, got %T", msg)
	}
	if result.UUID != "uuid-1" || result.APIErrorStatus != 429 {
		t.Fatalf("unexpected result fields: %#v", result)
	}
	if len(result.Errors) != 1 || result.Errors[0] != "oops" {
		t.Fatalf("unexpected errors: %#v", result.Errors)
	}
	if len(result.PermissionDenials) != 1 || result.ModelUsage["claude"] != float64(1) {
		t.Fatalf("unexpected extended fields: %#v", result)
	}
}
