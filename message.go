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

type ServerToolName string

const (
	ServerToolAdvisor                 ServerToolName = "advisor"
	ServerToolWebSearch               ServerToolName = "web_search"
	ServerToolWebFetch                ServerToolName = "web_fetch"
	ServerToolCodeExecution           ServerToolName = "code_execution"
	ServerToolBashCodeExecution       ServerToolName = "bash_code_execution"
	ServerToolTextEditorCodeExecution ServerToolName = "text_editor_code_execution"
	ServerToolSearchRegex             ServerToolName = "tool_search_tool_regex"
	ServerToolSearchBM25              ServerToolName = "tool_search_tool_bm25"
)

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

type TaskStartedMessage struct {
	SystemMessage
	TaskID      string
	Description string
	UUID        string
	SessionID   string
	ToolUseID   string
	TaskType    string
}

type TaskProgressMessage struct {
	SystemMessage
	TaskID       string
	Description  string
	Usage        map[string]any
	UUID         string
	SessionID    string
	ToolUseID    string
	LastToolName string
}

type TaskUsage struct {
	TotalTokens int `json:"total_tokens"`
	ToolUses    int `json:"tool_uses"`
	DurationMS  int `json:"duration_ms"`
}

type TaskNotificationStatus string

const (
	TaskNotificationStatusCompleted TaskNotificationStatus = "completed"
	TaskNotificationStatusFailed    TaskNotificationStatus = "failed"
	TaskNotificationStatusStopped   TaskNotificationStatus = "stopped"
)

type TaskNotificationMessage struct {
	SystemMessage
	TaskID     string
	Status     string
	OutputFile string
	Summary    string
	UUID       string
	SessionID  string
	ToolUseID  string
	Usage      map[string]any
}

type TaskUpdatedStatus string

const (
	TaskUpdatedStatusPending   TaskUpdatedStatus = "pending"
	TaskUpdatedStatusRunning   TaskUpdatedStatus = "running"
	TaskUpdatedStatusPaused    TaskUpdatedStatus = "paused"
	TaskUpdatedStatusCompleted TaskUpdatedStatus = "completed"
	TaskUpdatedStatusFailed    TaskUpdatedStatus = "failed"
	TaskUpdatedStatusKilled    TaskUpdatedStatus = "killed"
)

var TerminalTaskStatuses = map[string]struct{}{
	"completed": {},
	"failed":    {},
	"stopped":   {},
	"killed":    {},
}

type TaskUpdatedMessage struct {
	SystemMessage
	TaskID    string
	Patch     map[string]any
	Status    TaskUpdatedStatus
	SessionID string
	UUID      string
}

type MirrorErrorMessage struct {
	SystemMessage
	Key   any
	Error string
}

type DeferredToolUse struct {
	ID    string
	Name  string
	Input map[string]any
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
	DeferredToolUse   *DeferredToolUse
	Errors            []string
	APIErrorStatus    int
	UUID              string
}

func (m *ResultMessage) GetType() string { return "result" }

type RateLimitInfo struct {
	Status                string
	ResetsAt              int
	RateLimitType         string
	Utilization           float64
	OverageStatus         string
	OverageResetsAt       int
	OverageDisabledReason string
	Raw                   map[string]any
}

type RateLimitStatus string

const (
	RateLimitStatusAllowed        RateLimitStatus = "allowed"
	RateLimitStatusAllowedWarning RateLimitStatus = "allowed_warning"
	RateLimitStatusRejected       RateLimitStatus = "rejected"
)

type RateLimitType string

const (
	RateLimitTypeFiveHour       RateLimitType = "five_hour"
	RateLimitTypeSevenDay       RateLimitType = "seven_day"
	RateLimitTypeSevenDayOpus   RateLimitType = "seven_day_opus"
	RateLimitTypeSevenDaySonnet RateLimitType = "seven_day_sonnet"
	RateLimitTypeOverage        RateLimitType = "overage"
)

type RateLimitEvent struct {
	RateLimitInfo RateLimitInfo
	UUID          string
	SessionID     string
}

func (m *RateLimitEvent) GetType() string { return "rate_limit_event" }

type StreamEvent struct {
	UUID            string
	SessionID       string
	Event           map[string]any
	ParentToolUseID string
}

func (m *StreamEvent) GetType() string { return "stream_event" }

type UnknownMessage struct {
	Type string
	Raw  map[string]any
}

func (m *UnknownMessage) GetType() string { return m.Type }
