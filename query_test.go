package claudeagentsdk

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestQueryUsesEmptySessionIDLikePython(t *testing.T) {
	transport := newFakeTransport()
	client := NewClientWithTransport(Options{}, transport)

	done := make(chan error, 1)
	go func() {
		_, err := queryWithClient(context.Background(), "hello", Options{}, client)
		done <- err
	}()

	var initPayload map[string]any
	select {
	case raw := <-transport.writeCh:
		if err := json.Unmarshal(raw, &initPayload); err != nil {
			t.Fatalf("failed to decode initialize request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initialize request")
	}

	transport.messages <- transportMessage{
		Data: map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"request_id": initPayload["request_id"],
				"subtype":    "success",
				"response":   map[string]any{},
			},
		},
	}

	var userPayload map[string]any
	select {
	case raw := <-transport.writeCh:
		if err := json.Unmarshal(raw, &userPayload); err != nil {
			t.Fatalf("failed to decode user payload: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for user payload")
	}

	if got := userPayload["session_id"]; got != "" {
		t.Fatalf("expected empty session_id like python query(), got %#v", got)
	}

	select {
	case <-time.After(150 * time.Millisecond):
	case raw := <-transport.writeCh:
		t.Fatalf("did not expect EndInput-triggered extra write before result, got %s", string(raw))
	}

	transport.messages <- transportMessage{
		Data: map[string]any{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(1),
			"duration_api_ms": float64(1),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "",
		},
	}
	if err := <-done; err != nil {
		t.Fatalf("QueryWithClient() error = %v", err)
	}
}

func TestQueryKeepsInputOpenUntilBackgroundAgentCompletes(t *testing.T) {
	transport := newFakeTransport()
	client := NewClientWithTransport(Options{}, transport)

	done := make(chan error, 1)
	go func() {
		_, err := queryWithClient(context.Background(), "hello", Options{}, client)
		done <- err
	}()

	var initPayload map[string]any
	select {
	case raw := <-transport.writeCh:
		if err := json.Unmarshal(raw, &initPayload); err != nil {
			t.Fatalf("failed to decode initialize request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initialize request")
	}
	transport.messages <- transportMessage{Data: map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"request_id": initPayload["request_id"],
			"subtype":    "success",
			"response":   map[string]any{},
		},
	}}

	select {
	case <-transport.writeCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for user message")
	}
	if err := <-done; err != nil {
		t.Fatalf("queryWithClient() error = %v", err)
	}

	transport.messages <- transportMessage{Data: map[string]any{
		"type": "system", "subtype": "task_started", "task_id": "task-1", "task_type": "local_agent",
	}}
	transport.messages <- testResultMessage()

	select {
	case <-transport.endInput:
		t.Fatal("stdin closed while a background agent was still running")
	case <-time.After(100 * time.Millisecond):
	}

	transport.messages <- transportMessage{Data: map[string]any{
		"type": "system", "subtype": "task_updated", "task_id": "task-1", "patch": map[string]any{"status": "completed"},
	}}
	transport.messages <- testResultMessage()

	select {
	case <-transport.endInput:
	case <-time.After(time.Second):
		t.Fatal("stdin was not closed after the final result")
	}
}

func TestQueryStreamWritesEachInputAndEndsInput(t *testing.T) {
	transport := newFakeTransport()
	input := make(chan map[string]any, 2)
	done := make(chan error, 1)
	go func() {
		_, err := QueryStreamWithTransport(context.Background(), input, nil, transport)
		done <- err
	}()
	respondToControlRequest(t, transport, map[string]any{})
	if err := <-done; err != nil {
		t.Fatalf("QueryStreamWithTransport() error = %v", err)
	}

	input <- map[string]any{"type": "user", "message": map[string]any{"content": "one"}}
	input <- map[string]any{"type": "user", "message": map[string]any{"content": "two"}}
	close(input)
	for _, want := range []string{"one", "two"} {
		select {
		case raw := <-transport.writeCh:
			if !strings.Contains(string(raw), want) {
				t.Fatalf("expected input %q, got %s", want, raw)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for input %q", want)
		}
	}
	select {
	case <-transport.endInput:
	case <-time.After(time.Second):
		t.Fatal("expected streamed input to end stdin")
	}
}

func testResultMessage() transportMessage {
	return transportMessage{Data: map[string]any{
		"type": "result", "subtype": "success", "duration_ms": float64(1), "duration_api_ms": float64(1),
		"is_error": false, "num_turns": float64(1), "session_id": "",
	}}
}
