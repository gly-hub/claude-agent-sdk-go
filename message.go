package claudeagentsdk

type Message interface {
	GetType() string
}

type ContentBlock interface {
	BlockType() string
}

type TextBlock struct {
	Text string `json:"text"`
}

func (b TextBlock) BlockType() string { return "text" }

type ThinkingBlock struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

func (b ThinkingBlock) BlockType() string { return "thinking" }

type ToolUseBlock struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func (b ToolUseBlock) BlockType() string { return "tool_use" }

type ToolResultBlock struct {
	ToolUseID string `json:"tool_use_id"`
	Content   any    `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

func (b ToolResultBlock) BlockType() string { return "tool_result" }

type ServerToolUseBlock struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func (b ServerToolUseBlock) BlockType() string { return "server_tool_use" }

type ServerToolResultBlock struct {
	ToolUseID string `json:"tool_use_id"`
	Content   any    `json:"content"`
}

func (b ServerToolResultBlock) BlockType() string { return "advisor_tool_result" }

type UserMessage struct {
	Content         any
	UUID            string
	ParentToolUseID string
	ToolUseResult   any
}

func (m *UserMessage) GetType() string { return "user" }

type AssistantMessage struct {
	Content         []ContentBlock
	Model           string
	ParentToolUseID string
	Error           any
	Usage           map[string]any
	MessageID       string
	StopReason      string
	SessionID       string
	UUID            string
}

func (m *AssistantMessage) GetType() string { return "assistant" }

type SystemMessage struct {
	Subtype string
	Data    map[string]any
}

func (m *SystemMessage) GetType() string { return "system" }

type HookEventMessage struct {
	SystemMessage
	HookEventName string
	SessionID     string
	UUID          string
}

type ResultMessage struct {
	Subtype           string
	DurationMS        int
	DurationAPIMS     int
	IsError           bool
	NumTurns          int
	SessionID         string
	StopReason        string
	TotalCostUSD      float64
	Usage             map[string]any
	Result            string
	StructuredOutput  any
	ModelUsage        map[string]any
	PermissionDenials []any
	DeferredToolUse   any
	Errors            []string
	APIErrorStatus    int
	UUID              string
}

func (m *ResultMessage) GetType() string { return "result" }

type RateLimitEvent struct {
	Subtype       string
	RateLimitInfo map[string]any
	UUID          string
	SessionID     string
}

func (m *RateLimitEvent) GetType() string { return "rate_limit" }

type StreamEvent struct {
	Subtype   string
	Delta     string
	SessionID string
	UUID      string
	Raw       map[string]any
}

func (m *StreamEvent) GetType() string { return "stream_event" }

type UnknownMessage struct {
	Type string
	Raw  map[string]any
}

func (m *UnknownMessage) GetType() string { return m.Type }
