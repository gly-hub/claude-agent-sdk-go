package claudeagentsdk

import (
	"context"
	"encoding/json"
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
