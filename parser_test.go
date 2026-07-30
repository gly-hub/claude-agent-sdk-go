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
		"type":       "result",
		"subtype":    "success",
		"session_id": "sess-1",
		"uuid":       "uuid-1",
		"modelUsage": map[string]any{"claude": map[string]any{
			"inputTokens": float64(1), "outputTokens": float64(2), "canonicalModel": "claude-sonnet-4-5", "provider": "firstParty",
		}},
		"permission_denials": []any{map[string]any{"tool": "Bash"}},
		"deferred_tool_use":  map[string]any{"id": "tool-1", "name": "Bash", "input": map[string]any{"command": "pwd"}},
		"errors":             []any{"oops"},
		"api_error_status":   float64(429),
		"terminal_reason":    "completed",
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
	if len(result.PermissionDenials) != 1 || result.ModelUsage["claude"].InputTokens != 1 || result.ModelUsage["claude"].CanonicalModel != "claude-sonnet-4-5" {
		t.Fatalf("unexpected extended fields: %#v", result)
	}
	if result.TerminalReason != "completed" {
		t.Fatalf("unexpected terminal reason: %#v", result)
	}
	if result.DeferredToolUse == nil || result.DeferredToolUse.Name != "Bash" {
		t.Fatalf("unexpected deferred tool use: %#v", result.DeferredToolUse)
	}
}

func TestParseSystemTaskMessages(t *testing.T) {
	msg, err := ParseMessage(map[string]any{
		"type":        "system",
		"subtype":     "task_started",
		"task_id":     "task-1",
		"description": "Running",
		"uuid":        "uuid-1",
		"session_id":  "sess-1",
		"tool_use_id": "tool-1",
		"task_type":   "analysis",
	})
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	taskStarted, ok := msg.(*TaskStartedMessage)
	if !ok || taskStarted.TaskType != "analysis" {
		t.Fatalf("unexpected task started message: %#v", msg)
	}

	msg, err = ParseMessage(map[string]any{
		"type":       "system",
		"subtype":    "task_updated",
		"task_id":    "task-2",
		"patch":      map[string]any{"status": "killed", "end_time": "now"},
		"uuid":       "uuid-3",
		"session_id": "sess-1",
	})
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	taskUpdated, ok := msg.(*TaskUpdatedMessage)
	if !ok || taskUpdated.Status != TaskUpdatedStatusKilled {
		t.Fatalf("unexpected task updated message: %#v", msg)
	}
	if _, ok := TerminalTaskStatuses[string(taskUpdated.Status)]; !ok {
		t.Fatalf("expected %q to be terminal", taskUpdated.Status)
	}

	msg, err = ParseMessage(map[string]any{
		"type":       "system",
		"subtype":    "mirror_error",
		"key":        map[string]any{"session_id": "sess-1"},
		"error":      "failed",
		"uuid":       "uuid-2",
		"session_id": "sess-1",
	})
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	mirrorErr, ok := msg.(*MirrorErrorMessage)
	if !ok || mirrorErr.Error != "failed" {
		t.Fatalf("unexpected mirror error message: %#v", msg)
	}
}

func TestParseStreamAndRateLimitEvents(t *testing.T) {
	msg, err := ParseMessage(map[string]any{
		"type":               "stream_event",
		"uuid":               "uuid-1",
		"session_id":         "sess-1",
		"event":              map[string]any{"type": "message_delta"},
		"parent_tool_use_id": "tool-1",
	})
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	stream, ok := msg.(*StreamEvent)
	if !ok || stream.Event["type"] != "message_delta" {
		t.Fatalf("unexpected stream event: %#v", msg)
	}

	msg, err = ParseMessage(map[string]any{
		"type":       "rate_limit_event",
		"uuid":       "uuid-2",
		"session_id": "sess-1",
		"rate_limit_info": map[string]any{
			"status":        "ok",
			"resetsAt":      float64(1),
			"utilization":   0.25,
			"rateLimitType": "default",
		},
	})
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	rate, ok := msg.(*RateLimitEvent)
	if !ok || rate.RateLimitInfo.Status != "ok" {
		t.Fatalf("unexpected rate limit event: %#v", msg)
	}
}

func TestParseUnknownMessageSkipsLikePython(t *testing.T) {
	msg, err := ParseMessage(map[string]any{"type": "future_message"})
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	if msg != nil {
		t.Fatalf("expected unknown message to be skipped, got %#v", msg)
	}
}
