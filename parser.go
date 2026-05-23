package claudeagentsdk

import "fmt"

func ParseMessage(data map[string]any) (Message, error) {
	msgType, _ := data["type"].(string)
	if msgType == "" {
		return nil, fmt.Errorf("message missing type")
	}

	if msgType == "system" {
		subtype, _ := data["subtype"].(string)
		if subtype == "hook_started" || subtype == "hook_response" {
			return &HookEventMessage{
				SystemMessage: SystemMessage{Subtype: subtype, Data: data},
				HookEventName: stringFromAny(data["hook_event"]),
				SessionID:     stringFromAny(data["session_id"]),
				UUID:          stringFromAny(data["uuid"]),
			}, nil
		}
	}

	switch msgType {
	case "user":
		message, _ := data["message"].(map[string]any)
		content := message["content"]
		if blocks, ok := content.([]any); ok {
			content = parseContentBlocks(blocks)
		}
		return &UserMessage{
			Content:         content,
			UUID:            stringFromAny(data["uuid"]),
			ParentToolUseID: stringFromAny(data["parent_tool_use_id"]),
			ToolUseResult:   data["tool_use_result"],
		}, nil
	case "assistant":
		message, _ := data["message"].(map[string]any)
		rawBlocks, _ := message["content"].([]any)
		return &AssistantMessage{
			Content:         parseContentBlocks(rawBlocks),
			Model:           stringFromAny(message["model"]),
			ParentToolUseID: stringFromAny(data["parent_tool_use_id"]),
			Error:           data["error"],
			Usage:           mapFromAny(message["usage"]),
			MessageID:       stringFromAny(message["id"]),
			StopReason:      stringFromAny(message["stop_reason"]),
			SessionID:       stringFromAny(data["session_id"]),
			UUID:            stringFromAny(data["uuid"]),
		}, nil
	case "system":
		return &SystemMessage{
			Subtype: stringFromAny(data["subtype"]),
			Data:    data,
		}, nil
	case "result":
		return &ResultMessage{
			Subtype:           stringFromAny(data["subtype"]),
			DurationMS:        intFromAny(data["duration_ms"]),
			DurationAPIMS:     intFromAny(data["duration_api_ms"]),
			IsError:           boolFromAny(data["is_error"]),
			NumTurns:          intFromAny(data["num_turns"]),
			SessionID:         stringFromAny(data["session_id"]),
			StopReason:        stringFromAny(data["stop_reason"]),
			TotalCostUSD:      floatFromAny(data["total_cost_usd"]),
			Usage:             mapFromAny(data["usage"]),
			Result:            stringFromAny(data["result"]),
			StructuredOutput:  data["structured_output"],
			ModelUsage:        mapFromAny(data["model_usage"]),
			PermissionDenials: sliceFromAny(data["permission_denials"]),
			DeferredToolUse:   data["deferred_tool_use"],
			Errors:            stringSliceFromAny(data["errors"]),
			APIErrorStatus:    intFromAny(data["api_error_status"]),
			UUID:              stringFromAny(data["uuid"]),
		}, nil
	case "stream_event":
		return &StreamEvent{
			Subtype:   stringFromAny(data["subtype"]),
			Delta:     stringFromAny(data["delta"]),
			SessionID: stringFromAny(data["session_id"]),
			UUID:      stringFromAny(data["uuid"]),
			Raw:       data,
		}, nil
	case "rate_limit":
		return &RateLimitEvent{
			Subtype:       stringFromAny(data["subtype"]),
			RateLimitInfo: mapFromAny(data["rate_limit_info"]),
			UUID:          stringFromAny(data["uuid"]),
			SessionID:     stringFromAny(data["session_id"]),
		}, nil
	default:
		return &UnknownMessage{Type: msgType, Raw: data}, nil
	}
}

func parseContentBlocks(raw []any) []ContentBlock {
	blocks := make([]ContentBlock, 0, len(raw))
	for _, item := range raw {
		block, _ := item.(map[string]any)
		switch stringFromAny(block["type"]) {
		case "text":
			blocks = append(blocks, TextBlock{Text: stringFromAny(block["text"])})
		case "thinking":
			blocks = append(blocks, ThinkingBlock{
				Thinking:  stringFromAny(block["thinking"]),
				Signature: stringFromAny(block["signature"]),
			})
		case "tool_use":
			blocks = append(blocks, ToolUseBlock{
				ID:    stringFromAny(block["id"]),
				Name:  stringFromAny(block["name"]),
				Input: mapFromAny(block["input"]),
			})
		case "tool_result":
			blocks = append(blocks, ToolResultBlock{
				ToolUseID: stringFromAny(block["tool_use_id"]),
				Content:   block["content"],
				IsError:   boolFromAny(block["is_error"]),
			})
		case "server_tool_use":
			blocks = append(blocks, ServerToolUseBlock{
				ID:    stringFromAny(block["id"]),
				Name:  stringFromAny(block["name"]),
				Input: mapFromAny(block["input"]),
			})
		case "advisor_tool_result":
			blocks = append(blocks, ServerToolResultBlock{
				ToolUseID: stringFromAny(block["tool_use_id"]),
				Content:   block["content"],
			})
		}
	}
	return blocks
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return s
}

func mapFromAny(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func sliceFromAny(v any) []any {
	s, _ := v.([]any)
	return s
}

func stringSliceFromAny(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		if typed, ok := v.([]string); ok {
			return typed
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func boolFromAny(v any) bool {
	b, _ := v.(bool)
	return b
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func floatFromAny(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}
