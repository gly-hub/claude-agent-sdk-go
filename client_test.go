package claudeagentsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
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
			string(HookEventPreToolUse): {
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

	resp, err := client.handleHookCallback(context.Background(), map[string]any{
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

func TestTypedControlGetters(t *testing.T) {
	transport := newFakeTransport()
	client := NewClientWithTransport(Options{}, transport)
	go client.readLoop()

	mcpDone := make(chan *MCPStatusResponse, 1)
	errCh := make(chan error, 2)
	go func() {
		resp, err := client.GetMCPStatusResponse(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		mcpDone <- resp
	}()
	respondToControlRequest(t, transport, map[string]any{
		"mcpServers": []any{
			map[string]any{
				"name":   "fs",
				"status": "connected",
				"serverInfo": map[string]any{
					"name":    "filesystem",
					"version": "1.0.0",
				},
			},
		},
	})

	select {
	case err := <-errCh:
		t.Fatalf("GetMCPStatusResponse() error = %v", err)
	case resp := <-mcpDone:
		if len(resp.MCPServers) != 1 || resp.MCPServers[0].Status != MCPServerStatusConnected {
			t.Fatalf("unexpected MCP status response: %#v", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for MCP status response")
	}

	usageDone := make(chan *ContextUsageResponse, 1)
	go func() {
		resp, err := client.GetContextUsageResponse(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		usageDone <- resp
	}()
	respondToControlRequest(t, transport, map[string]any{
		"categories":           []any{map[string]any{"name": "messages", "tokens": float64(12), "color": "blue"}},
		"totalTokens":          float64(12),
		"maxTokens":            float64(100),
		"rawMaxTokens":         float64(200),
		"percentage":           12.0,
		"model":                "claude",
		"isAutoCompactEnabled": true,
		"memoryFiles":          []any{},
		"mcpTools":             []any{},
		"agents":               []any{},
		"gridRows":             []any{},
	})

	select {
	case err := <-errCh:
		t.Fatalf("GetContextUsageResponse() error = %v", err)
	case resp := <-usageDone:
		if resp.TotalTokens != 12 || len(resp.Categories) != 1 {
			t.Fatalf("unexpected context usage response: %#v", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for context usage response")
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

func TestReceiveResponseStreamStopsAtResult(t *testing.T) {
	client := NewClient(Options{})
	client.msgCh <- &AssistantMessage{Content: []ContentBlock{TextBlock{Text: "hi"}}}
	client.msgCh <- &ResultMessage{Subtype: "success"}
	client.msgCh <- &AssistantMessage{Content: []ContentBlock{TextBlock{Text: "after"}}}

	var messages []Message
	for msg := range client.ReceiveResponseStream(context.Background()) {
		messages = append(messages, msg)
	}
	if len(messages) != 2 {
		t.Fatalf("unexpected message count: %d", len(messages))
	}
	if _, ok := messages[1].(*ResultMessage); !ok {
		t.Fatalf("expected ResultMessage, got %T", messages[1])
	}
}

func TestConnectIncludesExcludeDynamicSections(t *testing.T) {
	transport := newFakeTransport()
	client := NewClientWithTransport(Options{
		SystemPromptPreset: &SystemPromptPreset{
			Preset:                 "claude_code",
			ExcludeDynamicSections: true,
		},
	}, transport)

	done := make(chan error, 1)
	go func() {
		done <- client.Connect(context.Background())
	}()

	var payload map[string]any
	select {
	case raw := <-transport.writeCh:
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("failed to decode control request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initialize request")
	}

	request := mapFromAny(payload["request"])
	if request["excludeDynamicSections"] != true {
		t.Fatalf("expected excludeDynamicSections in initialize request, got %#v", request)
	}

	transport.messages <- transportMessage{
		Data: map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"request_id": payload["request_id"],
				"subtype":    "success",
				"response":   map[string]any{"ok": true},
			},
		},
	}

	if err := <-done; err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
}

func TestConnectOmitsInitializeSkillsWhenSkillsAll(t *testing.T) {
	transport := newFakeTransport()
	client := NewClientWithTransport(Options{
		Skills: "all",
	}, transport)

	done := make(chan error, 1)
	go func() {
		done <- client.Connect(context.Background())
	}()

	var payload map[string]any
	select {
	case raw := <-transport.writeCh:
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("failed to decode control request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initialize request")
	}

	request := mapFromAny(payload["request"])
	if _, exists := request["skills"]; exists {
		t.Fatalf("expected skills to be omitted for skills=all, got %#v", request)
	}

	transport.messages <- transportMessage{
		Data: map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"request_id": payload["request_id"],
				"subtype":    "success",
				"response":   map[string]any{"ok": true},
			},
		},
	}

	if err := <-done; err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
}

func TestConnectIncludesInitializeSkillsWhenSkillsEmptyList(t *testing.T) {
	transport := newFakeTransport()
	client := NewClientWithTransport(Options{
		Skills: []string{},
	}, transport)

	done := make(chan error, 1)
	go func() {
		done <- client.Connect(context.Background())
	}()

	var payload map[string]any
	select {
	case raw := <-transport.writeCh:
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("failed to decode control request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initialize request")
	}

	request := mapFromAny(payload["request"])
	rawSkills, exists := request["skills"]
	if !exists {
		t.Fatalf("expected skills field for explicit empty list, got %#v", request)
	}
	skills, ok := rawSkills.([]any)
	if !ok || len(skills) != 0 {
		t.Fatalf("expected empty skills list, got %#v", rawSkills)
	}

	transport.messages <- transportMessage{
		Data: map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"request_id": payload["request_id"],
				"subtype":    "success",
				"response":   map[string]any{"ok": true},
			},
		},
	}

	if err := <-done; err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
}

func TestSendControlRequestReturnsProcessErrorBeforeTimeout(t *testing.T) {
	transport := newFakeTransport()
	client := NewClientWithTransport(Options{}, transport)
	go client.readLoop()

	done := make(chan error, 1)
	go func() {
		_, err := client.sendControlRequest(context.Background(), map[string]any{"subtype": "initialize"}, time.Hour)
		done <- err
	}()

	<-transport.writeCh
	processErr := &ProcessError{ExitCode: 2, Stderr: "boom"}
	transport.messages <- transportMessage{Err: processErr}

	select {
	case err := <-done:
		var got *ProcessError
		if !errors.As(err, &got) {
			t.Fatalf("expected ProcessError, got %T: %v", err, err)
		}
		if got.Stderr != "boom" {
			t.Fatalf("unexpected process error: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("sendControlRequest did not return process error before timeout")
	}
}

func TestSendControlRequestReturnsStreamClosedBeforeTimeout(t *testing.T) {
	transport := newFakeTransport()
	client := NewClientWithTransport(Options{}, transport)
	go client.readLoop()

	done := make(chan error, 1)
	go func() {
		_, err := client.sendControlRequest(context.Background(), map[string]any{"subtype": "initialize"}, time.Hour)
		done <- err
	}()

	<-transport.writeCh
	close(transport.messages)

	select {
	case err := <-done:
		if err == nil || err.Error() != "claude process stream closed before control response" {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sendControlRequest did not return stream closed before timeout")
	}
}

func TestControlCancelRequestSuppressesHookResponse(t *testing.T) {
	transport := newFakeTransport()
	callbackStarted := make(chan struct{})
	cancellationObserved := make(chan struct{})
	client := NewClientWithTransport(Options{}, transport)
	callbackID := client.registerHookCallback(func(_ map[string]any, _ string, ctx HookContext) (HookOutput, error) {
		close(callbackStarted)
		signal, ok := ctx.Signal.(<-chan struct{})
		if !ok {
			return nil, fmt.Errorf("unexpected hook signal type %T", ctx.Signal)
		}
		<-signal
		close(cancellationObserved)
		return nil, context.Canceled
	})
	go client.readLoop()

	transport.messages <- transportMessage{Data: map[string]any{
		"type":       "control_request",
		"request_id": "hook-request",
		"request": map[string]any{
			"subtype": "hook_callback", "callback_id": callbackID, "input": map[string]any{},
		},
	}}
	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		t.Fatal("hook callback did not start")
	}

	transport.messages <- transportMessage{Data: map[string]any{
		"type": "control_cancel_request", "request_id": "hook-request",
	}}
	select {
	case <-cancellationObserved:
	case <-time.After(time.Second):
		t.Fatal("hook callback did not receive the cancellation signal")
	}

	select {
	case raw := <-transport.writeCh:
		t.Fatalf("unexpected response after cancellation: %s", raw)
	case <-time.After(100 * time.Millisecond):
	}
	close(transport.messages)
}

type fakeTransport struct {
	messages chan transportMessage
	writeCh  chan []byte
	endInput chan struct{}
	ready    bool
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		messages: make(chan transportMessage, 4),
		writeCh:  make(chan []byte, 4),
		endInput: make(chan struct{}, 4),
		ready:    true,
	}
}

func (t *fakeTransport) Connect(context.Context) error { return nil }

func (t *fakeTransport) Write(_ context.Context, payload []byte) error {
	t.writeCh <- payload
	return nil
}

func (t *fakeTransport) ReadMessages() <-chan transportMessage { return t.messages }

func (t *fakeTransport) Close() error { return nil }

func (t *fakeTransport) EndInput() error {
	select {
	case t.endInput <- struct{}{}:
	default:
	}
	return nil
}

func (t *fakeTransport) IsReady() bool { return t.ready }

func respondToControlRequest(t *testing.T, transport *fakeTransport, response map[string]any) {
	t.Helper()
	select {
	case raw := <-transport.writeCh:
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("failed to decode control request: %v", err)
		}
		transport.messages <- transportMessage{
			Data: map[string]any{
				"type": "control_response",
				"response": map[string]any{
					"request_id": payload["request_id"],
					"subtype":    "success",
					"response":   response,
				},
			},
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for control request")
	}
}
