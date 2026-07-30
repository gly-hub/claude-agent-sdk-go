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

type SdkBeta string

const (
	SdkBetaContext1M20250807 SdkBeta = "context-1m-2025-08-07"
)

type SettingSource string

const (
	SettingSourceUser    SettingSource = "user"
	SettingSourceProject SettingSource = "project"
	SettingSourceLocal   SettingSource = "local"
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

type PermissionBehavior string

const (
	PermissionBehaviorAllow PermissionBehavior = "allow"
	PermissionBehaviorDeny  PermissionBehavior = "deny"
	PermissionBehaviorAsk   PermissionBehavior = "ask"
)

type PermissionUpdateDestination string

const (
	PermissionUpdateDestinationUserSettings    PermissionUpdateDestination = "userSettings"
	PermissionUpdateDestinationProjectSettings PermissionUpdateDestination = "projectSettings"
	PermissionUpdateDestinationLocalSettings   PermissionUpdateDestination = "localSettings"
	PermissionUpdateDestinationSession         PermissionUpdateDestination = "session"
)

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

type HookEvent string

const (
	HookEventPreToolUse         HookEvent = "PreToolUse"
	HookEventPostToolUse        HookEvent = "PostToolUse"
	HookEventPostToolUseFailure HookEvent = "PostToolUseFailure"
	HookEventUserPromptSubmit   HookEvent = "UserPromptSubmit"
	HookEventStop               HookEvent = "Stop"
	HookEventSubagentStop       HookEvent = "SubagentStop"
	HookEventPreCompact         HookEvent = "PreCompact"
	HookEventNotification       HookEvent = "Notification"
	HookEventSubagentStart      HookEvent = "SubagentStart"
	HookEventPermissionRequest  HookEvent = "PermissionRequest"
)

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
	// Data is SDK-owned opaque state maintained by FoldSessionSummary.
	Data map[string]any
}

type SessionListSubkeysKey struct {
	ProjectKey string
	SessionID  string
}

type SessionStore interface {
	Append(key SessionKey, entries []SessionStoreEntry) error
	Load(key SessionKey) ([]SessionStoreEntry, error)
}

// SessionListStore is an optional SessionStore capability used for listing sessions
// and resuming the most recent session with ContinueConversation.
type SessionListStore interface {
	ListSessions(projectKey string) ([]SessionStoreListEntry, error)
}

// SessionSubkeyStore is an optional SessionStore capability used for subagent transcripts.
type SessionSubkeyStore interface {
	ListSubkeys(key SessionListSubkeysKey) ([]string, error)
}

// SessionDeleteStore is an optional SessionStore capability for deleting transcripts.
type SessionDeleteStore interface {
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

type MCPSSEServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (MCPSSEServerConfig) mcpServerConfigMarker() {}

type MCPServerConnectionStatus string

const (
	MCPServerStatusConnected MCPServerConnectionStatus = "connected"
	MCPServerStatusFailed    MCPServerConnectionStatus = "failed"
	MCPServerStatusNeedsAuth MCPServerConnectionStatus = "needs-auth"
	MCPServerStatusPending   MCPServerConnectionStatus = "pending"
	MCPServerStatusDisabled  MCPServerConnectionStatus = "disabled"
)

type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type MCPToolInfo struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Annotations *MCPToolAnnotations `json:"annotations,omitempty"`
}

type MCPServerStatus struct {
	Name       string                    `json:"name"`
	Status     MCPServerConnectionStatus `json:"status"`
	ServerInfo *MCPServerInfo            `json:"serverInfo,omitempty"`
	Error      string                    `json:"error,omitempty"`
	Config     map[string]any            `json:"config,omitempty"`
	Scope      string                    `json:"scope,omitempty"`
	Tools      []MCPToolInfo             `json:"tools,omitempty"`
}

// MCPClaudeAIProxyServerConfig is the output-only configuration returned for
// MCP servers proxied through Claude.ai. MCPServerStatus.Config retains the
// raw wire object so all CLI configuration variants remain representable.
type MCPClaudeAIProxyServerConfig struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	ID   string `json:"id"`
}

type MCPStatusResponse struct {
	MCPServers []MCPServerStatus `json:"mcpServers"`
}

type ContextUsageCategory struct {
	Name       string `json:"name"`
	Tokens     int    `json:"tokens"`
	Color      string `json:"color"`
	IsDeferred bool   `json:"isDeferred,omitempty"`
}

type ContextUsageResponse struct {
	Categories           []ContextUsageCategory `json:"categories"`
	TotalTokens          int                    `json:"totalTokens"`
	MaxTokens            int                    `json:"maxTokens"`
	RawMaxTokens         int                    `json:"rawMaxTokens"`
	Percentage           float64                `json:"percentage"`
	Model                string                 `json:"model"`
	IsAutoCompactEnabled bool                   `json:"isAutoCompactEnabled"`
	MemoryFiles          []map[string]any       `json:"memoryFiles"`
	MCPTools             []map[string]any       `json:"mcpTools"`
	Agents               []map[string]any       `json:"agents"`
	GridRows             [][]map[string]any     `json:"gridRows"`
	AutoCompactThreshold int                    `json:"autoCompactThreshold,omitempty"`
	DeferredBuiltinTools []map[string]any       `json:"deferredBuiltinTools,omitempty"`
	SystemTools          []map[string]any       `json:"systemTools,omitempty"`
	SystemPromptSections []map[string]any       `json:"systemPromptSections,omitempty"`
	SlashCommands        map[string]any         `json:"slashCommands,omitempty"`
	SkillsUsage          map[string]any         `json:"skills,omitempty"`
	MessageBreakdown     map[string]any         `json:"messageBreakdown,omitempty"`
	APIUsage             map[string]any         `json:"apiUsage,omitempty"`
}

const PluginTypeLocal = "local"

type SDKPluginConfig struct {
	Type string
	Path string
}

type ExtraArgValue struct {
	Value  string
	IsFlag bool
}

type Options struct {
	CLIPath                 string
	SkipCLIVersionCheck     bool
	AllowCLIDownload        bool
	CLIVersion              string
	CLICacheDir             string
	CLIDownloadURL          string
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
	MaxTurnsSet             bool
	MaxBudgetUSD            float64
	MaxBudgetUSDSet         bool
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
	Skills                  any
	Plugins                 []SDKPluginConfig
	ExtraArgs               map[string]ExtraArgValue
	Thinking                *ThinkingConfig
	MaxThinkingTokens       int
	MaxThinkingTokensSet    bool
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
	// LoadTimeoutMSSet distinguishes an explicit zero timeout from the default 60 seconds.
	LoadTimeoutMSSet bool
	Stderr           func(string)
	// OnWarning receives non-fatal configuration diagnostics.
	OnWarning  func(string)
	CanUseTool CanUseToolFunc
}
