package claudeagentsdk

type PermissionMode string

const (
	PermissionModeDefault           PermissionMode = "default"
	PermissionModeAcceptEdits       PermissionMode = "acceptEdits"
	PermissionModePlan              PermissionMode = "plan"
	PermissionModeBypassPermissions PermissionMode = "bypassPermissions"
	PermissionModeDontAsk           PermissionMode = "dontAsk"
	PermissionModeAuto              PermissionMode = "auto"
)

type EffortLevel string

const (
	EffortLow    EffortLevel = "low"
	EffortMedium EffortLevel = "medium"
	EffortHigh   EffortLevel = "high"
	EffortXHigh  EffortLevel = "xhigh"
	EffortMax    EffortLevel = "max"
)

type ThinkingMode string

const (
	ThinkingAdaptive ThinkingMode = "adaptive"
	ThinkingEnabled  ThinkingMode = "enabled"
	ThinkingDisabled ThinkingMode = "disabled"
)

type ThinkingDisplay string

const (
	ThinkingDisplaySummarized ThinkingDisplay = "summarized"
	ThinkingDisplayOmitted    ThinkingDisplay = "omitted"
)

type ThinkingConfig struct {
	Mode         ThinkingMode
	BudgetTokens int
	Display      ThinkingDisplay
}

type ToolsPreset string

const (
	ToolsPresetClaudeCode ToolsPreset = "claude_code"
)

type SystemPromptPreset struct {
	Preset                 string
	Append                 string
	ExcludeDynamicSections bool
}

type SandboxSettings map[string]any

type OutputFormat struct {
	Type   string
	Schema any
}

type TaskBudget struct {
	Total int `json:"total"`
}

type AgentDefinition struct {
	Description     string         `json:"description"`
	Prompt          string         `json:"prompt"`
	Tools           []string       `json:"tools,omitempty"`
	DisallowedTools []string       `json:"disallowedTools,omitempty"`
	Model           string         `json:"model,omitempty"`
	Skills          []string       `json:"skills,omitempty"`
	Memory          string         `json:"memory,omitempty"`
	MCPServers      []any          `json:"mcpServers,omitempty"`
	InitialPrompt   string         `json:"initialPrompt,omitempty"`
	MaxTurns        int            `json:"maxTurns,omitempty"`
	Background      bool           `json:"background,omitempty"`
	Effort          any            `json:"effort,omitempty"`
	PermissionMode  PermissionMode `json:"permissionMode,omitempty"`
}

type PermissionRuleValue struct {
	ToolName    string `json:"toolName"`
	RuleContent string `json:"ruleContent,omitempty"`
}

type PermissionUpdate struct {
	Type        string                `json:"type"`
	Rules       []PermissionRuleValue `json:"rules,omitempty"`
	Behavior    string                `json:"behavior,omitempty"`
	Mode        PermissionMode        `json:"mode,omitempty"`
	Directories []string              `json:"directories,omitempty"`
	Destination string                `json:"destination,omitempty"`
}

type ToolPermissionRequest struct {
	ToolName              string
	Input                 map[string]any
	PermissionSuggestions []PermissionUpdate
	ToolUseID             string
	AgentID               string
	BlockedPath           string
	DecisionReason        string
	Title                 string
	DisplayName           string
	Description           string
}

type PermissionDecision struct {
	Behavior           string
	Message            string
	Interrupt          bool
	UpdatedInput       map[string]any
	UpdatedPermissions []PermissionUpdate
}

type CanUseToolFunc func(req ToolPermissionRequest) (PermissionDecision, error)

type HookContext struct {
	Signal any
}

type HookOutput map[string]any

type HookCallback func(input map[string]any, toolUseID string, ctx HookContext) (HookOutput, error)

type HookMatcher struct {
	Matcher string
	Hooks   []HookCallback
	Timeout float64
}

type SessionStoreFlushMode string

const (
	SessionStoreFlushBatched SessionStoreFlushMode = "batched"
	SessionStoreFlushEager   SessionStoreFlushMode = "eager"
)

type SessionKey struct {
	ProjectKey string
	SessionID  string
	Subpath    string
}

type SessionStoreEntry map[string]any

type SessionStoreListEntry struct {
	SessionID string
	MTime     int64
}

type SessionStoreSummary struct {
	SessionID    string
	Summary      string
	LastModified int64
	FileSize     int64
	CustomTitle  string
	FirstPrompt  string
	GitBranch    string
	CWD          string
	Tag          string
	CreatedAt    int64
}

type SessionListSubkeysKey struct {
	ProjectKey string
	SessionID  string
}

type SessionStore interface {
	Append(key SessionKey, entries []SessionStoreEntry) error
	Load(key SessionKey) ([]SessionStoreEntry, error)
	ListSessions(projectKey string) ([]SessionStoreListEntry, error)
	ListSubkeys(key SessionListSubkeysKey) ([]string, error)
	Delete(key SessionKey) error
}

type SessionSummaryStore interface {
	ListSessionSummaries(projectKey string) ([]SessionStoreSummary, error)
}

type MCPServerConfig interface {
	mcpServerConfigMarker()
}

type SDKMCPServerConfig struct {
	Type     string
	Name     string
	Instance *SDKMCPServer
}

func (SDKMCPServerConfig) mcpServerConfigMarker() {}

type MCPStdioServerConfig struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (MCPStdioServerConfig) mcpServerConfigMarker() {}

type MCPHTTPServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (MCPHTTPServerConfig) mcpServerConfigMarker() {}

const PluginTypeLocal = "local"

type SDKPluginConfig struct {
	Type string
	Path string
}

type Options struct {
	CLIPath                 string
	SkipCLIVersionCheck     bool
	CWD                     string
	Env                     map[string]string
	User                    string
	SystemPrompt            string
	SystemPromptPreset      *SystemPromptPreset
	SystemPromptFile        string
	AppendSystemPrompt      string
	Tools                   []string
	ToolsPreset             ToolsPreset
	AllowedTools            []string
	DisallowedTools         []string
	MaxTurns                int
	MaxBudgetUSD            float64
	Model                   string
	FallbackModel           string
	Betas                   []string
	PermissionMode          PermissionMode
	PermissionPromptTool    string
	ContinueConversation    bool
	Resume                  string
	SessionID               string
	Settings                string
	Sandbox                 SandboxSettings
	AddDirs                 []string
	MCPConfig               string
	MCPServers              map[string]MCPServerConfig
	IncludePartialMessages  bool
	IncludeHookEvents       bool
	StrictMCPConfig         bool
	ForkSession             bool
	SettingSources          []string
	Skills                  []string
	EnableAllSkills         bool
	Plugins                 []SDKPluginConfig
	ExtraArgs               map[string]string
	ExtraFlags              []string
	Thinking                *ThinkingConfig
	MaxThinkingTokens       int
	Effort                  EffortLevel
	OutputFormat            *OutputFormat
	EnableFileCheckpointing bool
	MaxBufferSize           int
	TaskBudget              *TaskBudget
	Agents                  map[string]AgentDefinition
	Hooks                   map[string][]HookMatcher
	SessionStore            SessionStore
	SessionStoreFlush       SessionStoreFlushMode
	LoadTimeoutMS           int
	Stderr                  func(string)
	CanUseTool              CanUseToolFunc
}
