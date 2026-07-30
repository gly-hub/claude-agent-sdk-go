package claudeagentsdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const minimumClaudeCodeVersion = "2.0.0"
const defaultMaxBufferSize = 1024 * 1024
const defaultClaudeInstallURL = "https://claude.ai/install.sh"

type SubprocessTransport struct {
	opts        Options
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	msgCh       chan transportMessage
	ready       bool
	writeMu     sync.Mutex
	closeOnce   sync.Once
	processMu   sync.Mutex
	processDone chan struct{}
	processErr  error
	stderrDone  chan struct{}
	stderrBuf   bytes.Buffer
}

func NewSubprocessTransport(opts Options) *SubprocessTransport {
	return &SubprocessTransport{
		opts:        opts,
		msgCh:       make(chan transportMessage, 128),
		processDone: make(chan struct{}),
		stderrDone:  make(chan struct{}),
	}
}

func (t *SubprocessTransport) Connect(ctx context.Context) error {
	if t.ready {
		return nil
	}
	if err := validatePluginConfigs(t.opts.Plugins); err != nil {
		return err
	}
	if err := validateBufferSize(t.opts.MaxBufferSize); err != nil {
		return err
	}
	if err := validatePermissionPromptOptions(t.opts); err != nil {
		return err
	}

	cliPath, err := t.resolveCLIPath(ctx)
	if err != nil {
		return err
	}
	if err := validateCLIPath(runtime.GOOS, cliPath); err != nil {
		return err
	}
	if err := validateCLIArguments(runtime.GOOS, t.opts.Resume, t.opts.SessionID); err != nil {
		return err
	}
	if !t.opts.SkipCLIVersionCheck {
		if err := checkClaudeVersion(ctx, cliPath); err != nil {
			return err
		}
	}

	cmdArgs := t.buildCommand(cliPath)
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	if t.opts.CWD != "" {
		cmd.Dir = t.opts.CWD
	}
	cmd.Env = t.buildEnv(ctx)
	if err := applyCommandUser(cmd, t.opts.User); err != nil {
		return err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	var stderr io.ReadCloser
	if t.opts.Stderr != nil {
		stderr, err = cmd.StderrPipe()
		if err != nil {
			return err
		}
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	t.cmd = cmd
	t.stdin = stdin
	t.stdout = stdout
	t.stderr = stderr
	t.processDone = make(chan struct{})
	t.processErr = nil
	t.stderrDone = make(chan struct{})
	t.ready = true

	go t.readStdout()
	if stderr != nil {
		go t.readStderr()
	} else {
		close(t.stderrDone)
	}
	go t.waitProcess()
	return nil
}

func (t *SubprocessTransport) Write(_ context.Context, payload []byte) error {
	if !t.ready || t.stdin == nil {
		return ErrNotConnected
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err := t.stdin.Write(payload)
	return err
}

func (t *SubprocessTransport) ReadMessages() <-chan transportMessage {
	return t.msgCh
}

func (t *SubprocessTransport) EndInput() error {
	if t.stdin == nil {
		return nil
	}
	return t.stdin.Close()
}

func (t *SubprocessTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		if t.stdin != nil {
			_ = t.stdin.Close()
		}
		if t.cmd != nil && t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
			if t.processDone != nil {
				<-t.processDone
				err = t.getProcessErr()
			}
		}
	})
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (t *SubprocessTransport) IsReady() bool {
	return t.ready
}

func (t *SubprocessTransport) resolveCLIPath(ctx context.Context) (string, error) {
	if t.opts.CLIPath != "" {
		return t.opts.CLIPath, nil
	}
	if bundled := findBundledCLI(); bundled != "" {
		return bundled, nil
	}
	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}
	for _, candidate := range candidateCLIPaths() {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if t.opts.AllowCLIDownload {
		return t.downloadCLI(ctx)
	}
	return "", fmt.Errorf("claude CLI not found; install it, set Options.CLIPath, or enable Options.AllowCLIDownload")
}

func candidateCLIPaths() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return []string{"/usr/local/bin/claude"}
	}
	return []string{
		filepath.Join(home, ".npm-global/bin/claude"),
		"/usr/local/bin/claude",
		filepath.Join(home, ".local/bin/claude"),
		filepath.Join(home, "node_modules/.bin/claude"),
		filepath.Join(home, ".yarn/bin/claude"),
		filepath.Join(home, ".claude/local/claude"),
	}
}

func findBundledCLI() string {
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	path := filepath.Join(filepath.Dir(exe), "_bundled", name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func (t *SubprocessTransport) downloadCLI(ctx context.Context) (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("automatic Claude CLI download is not supported on Windows yet; set Options.CLIPath")
	}

	version, err := validateCLIVersion(t.effectiveCLIVersion())
	if err != nil {
		return "", err
	}
	cliPath, err := t.cachedCLIPathForVersion(version)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(cliPath); err == nil && !info.IsDir() {
		return cliPath, nil
	}

	cacheDir := filepath.Dir(cliPath)
	homeDir := filepath.Join(cacheDir, "home")
	binDir := filepath.Join(homeDir, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("creating Claude CLI cache directory: %w", err)
	}

	scriptPath := filepath.Join(cacheDir, "install.sh")
	if err := t.downloadInstallScript(ctx, scriptPath); err != nil {
		return "", err
	}

	args := []string{scriptPath}
	if version != "latest" {
		args = append(args, version)
	}
	cmd := exec.CommandContext(ctx, "bash", args...)
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(homeDir, ".cache"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("installing Claude CLI: %w: %s", err, strings.TrimSpace(string(output)))
	}

	installed := filepath.Join(binDir, "claude")
	if info, err := os.Stat(installed); err != nil || info.IsDir() {
		return "", fmt.Errorf("Claude CLI installer completed but %s was not created", installed)
	}
	if err := copyFile(installed, cliPath, 0o755); err != nil {
		return "", fmt.Errorf("caching Claude CLI: %w", err)
	}
	return cliPath, nil
}

func (t *SubprocessTransport) downloadInstallScript(ctx context.Context, path string) error {
	url := t.opts.CLIDownloadURL
	if url == "" {
		url = defaultClaudeInstallURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating Claude CLI download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading Claude CLI installer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("downloading Claude CLI installer: unexpected HTTP status %s", resp.Status)
	}

	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return fmt.Errorf("creating Claude CLI installer file: %w", err)
	}
	_, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("writing Claude CLI installer: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing Claude CLI installer: %w", closeErr)
	}
	if err := os.Chmod(tmpPath, 0o700); err != nil {
		return fmt.Errorf("marking Claude CLI installer executable: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("installing Claude CLI installer file: %w", err)
	}
	return nil
}

func (t *SubprocessTransport) cachedCLIPath() (string, error) {
	version, err := validateCLIVersion(t.effectiveCLIVersion())
	if err != nil {
		return "", err
	}
	return t.cachedCLIPathForVersion(version)
}

func (t *SubprocessTransport) cachedCLIPathForVersion(version string) (string, error) {
	cacheRoot := t.opts.CLICacheDir
	if cacheRoot == "" {
		userCache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locating user cache directory: %w", err)
		}
		cacheRoot = filepath.Join(userCache, "claude-agent-sdk-go", "claude-cli")
	}
	platform := runtime.GOOS + "_" + runtime.GOARCH
	return filepath.Join(cacheRoot, sanitizePathComponent(version), platform, "claude"), nil
}

func (t *SubprocessTransport) effectiveCLIVersion() string {
	if t.opts.CLIVersion == "" {
		return "latest"
	}
	return t.opts.CLIVersion
}

func sanitizePathComponent(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_")
	value = strings.TrimSpace(replacer.Replace(value))
	if value == "" {
		return "latest"
	}
	return value
}

var cliVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.+-]+)?$`)

func validateCLIVersion(version string) (string, error) {
	version = strings.TrimSpace(version)
	if version == "latest" || version == "stable" || cliVersionPattern.MatchString(version) {
		return version, nil
	}
	return "", fmt.Errorf("invalid Claude CLI version %q: expected latest, stable, or a concrete semantic version", version)
}

func validateCLIPath(goos string, path string) error {
	if goos != "windows" {
		return nil
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".bat" || extension == ".cmd" {
		return fmt.Errorf("refusing to execute Windows batch CLI %q; use claude.exe or an explicit native executable", path)
	}
	return nil
}

func validateCLIArguments(goos string, resume string, sessionID string) error {
	if goos != "windows" {
		return nil
	}
	for name, value := range map[string]string{"resume": resume, "session_id": sessionID} {
		if strings.ContainsAny(value, "&|<>^%!\"\r\n") {
			return fmt.Errorf("%s contains characters unsafe for Windows command execution", name)
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func checkClaudeVersion(ctx context.Context, cliPath string) error {
	output, err := exec.CommandContext(ctx, cliPath, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("checking claude CLI version: %w", err)
	}
	version := extractVersion(string(output))
	if version == "" {
		return fmt.Errorf("could not parse claude CLI version from %q", strings.TrimSpace(string(output)))
	}
	if compareVersions(version, minimumClaudeCodeVersion) < 0 {
		return fmt.Errorf("claude CLI version %s is below required %s", version, minimumClaudeCodeVersion)
	}
	return nil
}

func extractVersion(s string) string {
	re := regexp.MustCompile(`\d+\.\d+\.\d+`)
	return re.FindString(s)
}

func compareVersions(a, b string) int {
	ap := versionParts(a)
	bp := versionParts(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
}

func versionParts(version string) [3]int {
	var out [3]int
	parts := strings.Split(version, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		out[i], _ = strconv.Atoi(parts[i])
	}
	return out
}

func (t *SubprocessTransport) buildCommand(cliPath string) []string {
	cmd := []string{cliPath, "--output-format", "stream-json", "--verbose"}

	switch {
	case t.opts.SystemPromptFile != "":
		cmd = append(cmd, "--system-prompt-file", t.opts.SystemPromptFile)
	case t.opts.AppendSystemPrompt != "":
		cmd = append(cmd, "--append-system-prompt", t.opts.AppendSystemPrompt)
	case t.opts.SystemPromptPreset != nil && t.opts.SystemPromptPreset.Append != "":
		cmd = append(cmd, "--append-system-prompt", t.opts.SystemPromptPreset.Append)
	case t.opts.SystemPromptPreset != nil:
		// Claude Code's preset prompt is the CLI default, so no flag is needed.
	default:
		cmd = append(cmd, "--system-prompt", t.opts.SystemPrompt)
	}

	if t.opts.Tools != nil {
		cmd = append(cmd, "--tools", strings.Join(t.opts.Tools, ","))
	} else if t.opts.ToolsPreset != "" {
		cmd = append(cmd, "--tools", "default")
	}
	if allowed := t.effectiveAllowedTools(); len(allowed) > 0 {
		cmd = append(cmd, "--allowedTools", strings.Join(allowed, ","))
	}
	if len(t.opts.DisallowedTools) > 0 {
		cmd = append(cmd, "--disallowedTools", strings.Join(t.opts.DisallowedTools, ","))
	}
	if t.opts.MaxTurns > 0 {
		cmd = append(cmd, "--max-turns", fmt.Sprintf("%d", t.opts.MaxTurns))
	}
	if t.opts.MaxBudgetUSD > 0 {
		cmd = append(cmd, "--max-budget-usd", fmt.Sprintf("%g", t.opts.MaxBudgetUSD))
	}
	if t.opts.TaskBudget != nil && t.opts.TaskBudget.Total > 0 {
		cmd = append(cmd, "--task-budget", fmt.Sprintf("%d", t.opts.TaskBudget.Total))
	}
	if t.opts.Model != "" {
		cmd = append(cmd, "--model", t.opts.Model)
	}
	if t.opts.FallbackModel != "" {
		cmd = append(cmd, "--fallback-model", t.opts.FallbackModel)
	}
	if len(t.opts.Betas) > 0 {
		cmd = append(cmd, "--betas", strings.Join(t.opts.Betas, ","))
	}
	if tool := t.effectivePermissionPromptTool(); tool != "" {
		cmd = append(cmd, "--permission-prompt-tool", tool)
	}
	if t.opts.PermissionMode != "" {
		cmd = append(cmd, "--permission-mode", string(t.opts.PermissionMode))
	}
	if t.opts.ContinueConversation {
		cmd = append(cmd, "--continue")
	}
	if t.opts.Resume != "" {
		cmd = append(cmd, "--resume="+t.opts.Resume)
	}
	if t.opts.SessionID != "" {
		cmd = append(cmd, "--session-id="+t.opts.SessionID)
	}
	if settings := t.buildSettingsValue(); settings != "" {
		cmd = append(cmd, "--settings", settings)
	}
	for _, dir := range t.opts.AddDirs {
		cmd = append(cmd, "--add-dir", dir)
	}
	if t.opts.MCPConfig != "" {
		cmd = append(cmd, "--mcp-config", t.opts.MCPConfig)
	} else if len(t.opts.MCPServers) > 0 {
		servers := map[string]any{}
		for name, config := range t.opts.MCPServers {
			encoded, err := marshalMCPServerConfig(config)
			if err != nil {
				continue
			}
			servers[name] = encoded
		}
		if len(servers) > 0 {
			body, _ := json.Marshal(map[string]any{"mcpServers": servers})
			cmd = append(cmd, "--mcp-config", string(body))
		}
	}
	if t.opts.IncludePartialMessages {
		cmd = append(cmd, "--include-partial-messages")
	}
	if t.opts.IncludeHookEvents {
		cmd = append(cmd, "--include-hook-events")
	}
	if t.opts.StrictMCPConfig {
		cmd = append(cmd, "--strict-mcp-config")
	}
	if t.opts.ForkSession {
		cmd = append(cmd, "--fork-session")
	}
	if t.opts.SessionStore != nil {
		cmd = append(cmd, "--session-mirror")
	}
	if sources := t.effectiveSettingSources(); sources != nil {
		cmd = append(cmd, "--setting-sources="+strings.Join(sources, ","))
	}
	for _, plugin := range t.opts.Plugins {
		if plugin.Type == PluginTypeLocal {
			cmd = append(cmd, "--plugin-dir", plugin.Path)
		}
	}
	for flag, value := range t.opts.ExtraArgs {
		if value.IsFlag {
			cmd = append(cmd, "--"+flag)
			continue
		}
		if strings.HasPrefix(value.Value, "-") {
			cmd = append(cmd, "--"+flag+"="+value.Value)
			continue
		}
		cmd = append(cmd, "--"+flag, value.Value)
	}
	if t.opts.Thinking != nil {
		switch t.opts.Thinking.Mode {
		case ThinkingAdaptive:
			cmd = append(cmd, "--thinking", "adaptive")
		case ThinkingEnabled:
			cmd = append(cmd, "--max-thinking-tokens", fmt.Sprintf("%d", t.opts.Thinking.BudgetTokens))
		case ThinkingDisabled:
			cmd = append(cmd, "--thinking", "disabled")
		}
		if t.opts.Thinking.Display != "" && t.opts.Thinking.Mode != ThinkingDisabled {
			cmd = append(cmd, "--thinking-display", string(t.opts.Thinking.Display))
		}
	} else if t.opts.MaxThinkingTokens > 0 {
		cmd = append(cmd, "--max-thinking-tokens", fmt.Sprintf("%d", t.opts.MaxThinkingTokens))
	}
	if t.opts.Effort != "" {
		cmd = append(cmd, "--effort", string(t.opts.Effort))
	}
	if t.opts.OutputFormat != nil && t.opts.OutputFormat.Type == "json_schema" && t.opts.OutputFormat.Schema != nil {
		if schema, err := json.Marshal(t.opts.OutputFormat.Schema); err == nil {
			cmd = append(cmd, "--json-schema", string(schema))
		}
	}

	cmd = append(cmd, "--input-format", "stream-json")
	return cmd
}

func (t *SubprocessTransport) buildSettingsValue() string {
	if len(t.opts.Sandbox) == 0 {
		return t.opts.Settings
	}

	settings := map[string]any{}
	if t.opts.Settings != "" {
		trimmed := strings.TrimSpace(t.opts.Settings)
		if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
			_ = json.Unmarshal([]byte(trimmed), &settings)
		} else if data, err := os.ReadFile(trimmed); err == nil {
			_ = json.Unmarshal(data, &settings)
		}
	}
	settings["sandbox"] = map[string]any(t.opts.Sandbox)
	body, err := json.Marshal(settings)
	if err != nil {
		return t.opts.Settings
	}
	return string(body)
}

func validatePluginConfigs(plugins []SDKPluginConfig) error {
	for i, plugin := range plugins {
		if plugin.Type != PluginTypeLocal {
			return fmt.Errorf("unsupported plugin type at index %d: %q", i, plugin.Type)
		}
		if plugin.Path == "" {
			return fmt.Errorf("plugin path at index %d is empty", i)
		}
	}
	return nil
}

func validateBufferSize(size int) error {
	if size < 0 {
		return fmt.Errorf("max buffer size cannot be negative: %d", size)
	}
	return nil
}

func validatePermissionPromptOptions(opts Options) error {
	if opts.CanUseTool != nil && opts.PermissionPromptTool != "" && opts.PermissionPromptTool != "stdio" {
		return fmt.Errorf("CanUseTool cannot be used with PermissionPromptTool %q; use one or the other", opts.PermissionPromptTool)
	}
	return nil
}

func (t *SubprocessTransport) effectivePermissionPromptTool() string {
	if t.opts.CanUseTool != nil && t.opts.PermissionPromptTool == "" {
		return "stdio"
	}
	return t.opts.PermissionPromptTool
}

func (t *SubprocessTransport) effectiveAllowedTools() []string {
	return effectiveAllowedTools(t.opts)
}

func effectiveAllowedTools(opts Options) []string {
	allowed := append([]string{}, opts.AllowedTools...)
	seen := map[string]struct{}{}
	for _, tool := range allowed {
		seen[tool] = struct{}{}
	}
	switch skills := opts.Skills.(type) {
	case string:
		if skills == "all" {
			if _, ok := seen["Skill"]; !ok {
				allowed = append(allowed, "Skill")
				seen["Skill"] = struct{}{}
			}
		}
	case []string:
		for _, skill := range skills {
			pattern := "Skill(" + skill + ")"
			if _, ok := seen[pattern]; ok {
				continue
			}
			allowed = append(allowed, pattern)
			seen[pattern] = struct{}{}
		}
	}
	return allowed
}

func canUseToolShadowedWarning(opts Options) string {
	if opts.CanUseTool == nil {
		return ""
	}
	if opts.PermissionMode == PermissionModeBypassPermissions {
		return "CanUseTool will not be invoked: PermissionModeBypassPermissions auto-approves every tool call (except explicit deny rules) before the callback is consulted. To gate every tool call, use a PreToolUse hook instead."
	}

	shadowed := make([]string, 0)
	seen := make(map[string]struct{})
	for _, entry := range effectiveAllowedTools(opts) {
		tool := wholeToolAllowed(entry)
		if tool == "" {
			continue
		}
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		shadowed = append(shadowed, tool)
	}
	if len(shadowed) == 0 {
		return ""
	}
	return fmt.Sprintf("CanUseTool will not be invoked for: %s. An AllowedTools entry that allows a whole tool auto-approves it before the callback is consulted. To gate every tool call, use a PreToolUse hook; or narrow the entry so calls fall through to CanUseTool. Allow rules from settings files can also shadow the callback but are not visible here.", strings.Join(shadowed, ", "))
}

func wholeToolAllowed(entry string) string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return ""
	}
	open := strings.IndexByte(entry, '(')
	if open < 0 {
		return entry
	}
	if open == 0 || !strings.HasSuffix(entry, ")") {
		return ""
	}
	if specifier := entry[open+1 : len(entry)-1]; specifier == "" || specifier == "*" {
		return entry[:open]
	}
	return ""
}

func callWarningCallback(callback func(string), warning string) {
	defer func() {
		// Diagnostics must not prevent the client from connecting.
		_ = recover()
	}()
	callback(warning)
}

func (t *SubprocessTransport) effectiveSettingSources() []string {
	if t.opts.SettingSources != nil {
		return t.opts.SettingSources
	}
	switch skills := t.opts.Skills.(type) {
	case string:
		if skills == "all" {
			return []string{"user", "project"}
		}
	case []string:
		if len(skills) > 0 {
			return []string{"user", "project"}
		}
	}
	if t.opts.Skills != nil {
		return []string{"user", "project"}
	}
	return nil
}

func (t *SubprocessTransport) buildEnv(ctx context.Context) []string {
	envMap := map[string]string{}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok && key != "CLAUDECODE" {
			envMap[key] = value
		}
	}
	envMap["CLAUDE_CODE_ENTRYPOINT"] = "sdk-go"
	if t.opts.EnableFileCheckpointing {
		envMap["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
	}
	if t.opts.IncludePartialMessages {
		envMap["CLAUDE_CODE_ENABLE_FINE_GRAINED_TOOL_STREAMING"] = "1"
	}
	if t.opts.CWD != "" {
		envMap["PWD"] = t.opts.CWD
	}
	for k, v := range t.opts.Env {
		envMap[k] = v
	}
	t.injectOTELTraceContext(ctx, envMap)
	envMap["CLAUDE_AGENT_SDK_VERSION"] = sdkVersion
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	return env
}

func (t *SubprocessTransport) injectOTELTraceContext(ctx context.Context, envMap map[string]string) {
	if ctx == nil {
		return
	}

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	traceparent, ok := carrier["traceparent"]
	if !ok || traceparent == "" {
		return
	}

	for _, key := range []string{"TRACEPARENT", "TRACESTATE"} {
		if _, overridden := t.opts.Env[key]; !overridden {
			delete(envMap, key)
		}
	}

	for key, value := range carrier {
		upperKey := strings.ToUpper(key)
		if _, overridden := t.opts.Env[upperKey]; overridden {
			continue
		}
		envMap[upperKey] = value
	}
}

func (t *SubprocessTransport) readStdout() {
	defer close(t.msgCh)

	reader := bufio.NewReader(t.stdout)
	framer := ndjsonFramer{}
	chunk := make([]byte, 32*1024)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			for _, line := range framer.Push(chunk[:n]) {
				if len(line) > t.effectiveMaxBufferSize() {
					t.msgCh <- transportMessage{Err: fmt.Errorf("json message exceeded maximum buffer size of %d bytes", t.effectiveMaxBufferSize())}
					return
				}
				payload, parseErr := parseNDJSONLine(line)
				if parseErr != nil {
					t.msgCh <- transportMessage{Err: parseErr}
					return
				}
				if payload != nil {
					t.msgCh <- transportMessage{Data: payload}
				}
			}
			if framer.PendingLen() > t.effectiveMaxBufferSize() {
				t.msgCh <- transportMessage{Err: fmt.Errorf("json message exceeded maximum buffer size of %d bytes", t.effectiveMaxBufferSize())}
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.msgCh <- transportMessage{Err: err}
				return
			}
			if tail := framer.Flush(); len(tail) > 0 {
				if len(tail) > t.effectiveMaxBufferSize() {
					t.msgCh <- transportMessage{Err: fmt.Errorf("json message exceeded maximum buffer size of %d bytes", t.effectiveMaxBufferSize())}
					return
				}
				if payload, parseErr := parseNDJSONLine(tail); parseErr == nil && payload != nil {
					t.msgCh <- transportMessage{Data: payload}
				}
			}
			if processErr := t.waitForProcessErr(); processErr != nil {
				t.msgCh <- transportMessage{Err: t.processError(processErr)}
			}
			return
		}
	}
}

// ndjsonFramer reconstructs lines from arbitrary process stdout chunks.
// Chunks must not be trimmed: a boundary can fall inside a JSON string.
type ndjsonFramer struct {
	pending []byte
}

func (f *ndjsonFramer) Push(chunk []byte) [][]byte {
	f.pending = append(f.pending, chunk...)
	lines := make([][]byte, 0)
	for {
		index := bytes.IndexByte(f.pending, '\n')
		if index < 0 {
			return lines
		}
		line := append([]byte(nil), f.pending[:index]...)
		lines = append(lines, line)
		f.pending = f.pending[index+1:]
	}
}

func (f *ndjsonFramer) PendingLen() int {
	return len(f.pending)
}

func (f *ndjsonFramer) Flush() []byte {
	tail := f.pending
	f.pending = nil
	return tail
}

func parseNDJSONLine(line []byte) (map[string]any, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || !bytes.HasPrefix(line, []byte("{")) {
		return nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(line, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (t *SubprocessTransport) effectiveMaxBufferSize() int {
	if t.opts.MaxBufferSize > 0 {
		return t.opts.MaxBufferSize
	}
	return defaultMaxBufferSize
}

func (t *SubprocessTransport) readStderr() {
	defer close(t.stderrDone)

	reader := bufio.NewReader(t.stderr)
	framer := ndjsonFramer{}
	chunk := make([]byte, 32*1024)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			for _, line := range framer.Push(chunk[:n]) {
				t.consumeStderrLine(string(line))
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if tail := framer.Flush(); len(tail) > 0 {
					t.consumeStderrLine(string(tail))
				}
			}
			return
		}
	}
}

func (t *SubprocessTransport) consumeStderrLine(line string) {
	t.stderrBuf.WriteString(line)
	t.stderrBuf.WriteByte('\n')
	if t.opts.Stderr != nil {
		callStderrCallback(t.opts.Stderr, line)
	}
}

func callStderrCallback(callback func(string), line string) {
	defer func() {
		// Stderr callbacks are observational and must not interrupt transport cleanup.
		_ = recover()
	}()
	callback(line)
}

func (t *SubprocessTransport) waitProcess() {
	err := t.cmd.Wait()
	if t.stderrDone != nil {
		<-t.stderrDone
	}
	t.processMu.Lock()
	t.processErr = err
	t.ready = false
	t.processMu.Unlock()
	close(t.processDone)
}

func (t *SubprocessTransport) waitForProcessErr() error {
	if t.processDone != nil {
		<-t.processDone
	}
	return t.getProcessErr()
}

func (t *SubprocessTransport) getProcessErr() error {
	t.processMu.Lock()
	defer t.processMu.Unlock()
	return t.processErr
}

func (t *SubprocessTransport) processError(err error) error {
	exitCode := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	return &ProcessError{
		ExitCode: exitCode,
		Stderr:   strings.TrimSpace(t.stderrBuf.String()),
	}
}
