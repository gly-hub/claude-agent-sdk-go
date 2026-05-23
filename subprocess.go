package claudeagentsdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const minimumClaudeCodeVersion = "2.0.0"
const defaultMaxBufferSize = 1024 * 1024

type SubprocessTransport struct {
	opts      Options
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	msgCh     chan transportMessage
	ready     bool
	writeMu   sync.Mutex
	closeOnce sync.Once
	stderrBuf bytes.Buffer
}

func NewSubprocessTransport(opts Options) *SubprocessTransport {
	return &SubprocessTransport{
		opts:  opts,
		msgCh: make(chan transportMessage, 128),
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

	cliPath, err := t.resolveCLIPath()
	if err != nil {
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
	cmd.Env = t.buildEnv()

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
	t.ready = true

	go t.readStdout()
	if stderr != nil {
		go t.readStderr()
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
			err = t.cmd.Wait()
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

func (t *SubprocessTransport) resolveCLIPath() (string, error) {
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
	return "", fmt.Errorf("claude CLI not found; install it or set Options.CLIPath")
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
	if t.opts.PermissionPromptTool != "" {
		cmd = append(cmd, "--permission-prompt-tool", t.opts.PermissionPromptTool)
	}
	if t.opts.PermissionMode != "" {
		cmd = append(cmd, "--permission-mode", string(t.opts.PermissionMode))
	}
	if t.opts.ContinueConversation {
		cmd = append(cmd, "--continue")
	}
	if t.opts.Resume != "" {
		cmd = append(cmd, "--resume", t.opts.Resume)
	}
	if t.opts.SessionID != "" {
		cmd = append(cmd, "--session-id", t.opts.SessionID)
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
	if sources := t.effectiveSettingSources(); len(sources) > 0 {
		cmd = append(cmd, "--setting-sources="+strings.Join(sources, ","))
	}
	for _, plugin := range t.opts.Plugins {
		if plugin.Type == PluginTypeLocal {
			cmd = append(cmd, "--plugin-dir", plugin.Path)
		}
	}
	for flag, value := range t.opts.ExtraArgs {
		cmd = append(cmd, "--"+flag, value)
	}
	for _, flag := range t.opts.ExtraFlags {
		cmd = append(cmd, "--"+flag)
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

func (t *SubprocessTransport) effectiveAllowedTools() []string {
	allowed := append([]string{}, t.opts.AllowedTools...)
	if t.opts.EnableAllSkills {
		allowed = append(allowed, "Skill")
	}
	for _, skill := range t.opts.Skills {
		allowed = append(allowed, "Skill("+skill+")")
	}
	return allowed
}

func (t *SubprocessTransport) effectiveSettingSources() []string {
	if len(t.opts.SettingSources) > 0 {
		return t.opts.SettingSources
	}
	if t.opts.EnableAllSkills || len(t.opts.Skills) > 0 {
		return []string{"user", "project"}
	}
	return nil
}

func (t *SubprocessTransport) buildEnv() []string {
	envMap := map[string]string{}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok && key != "CLAUDECODE" {
			envMap[key] = value
		}
	}
	envMap["CLAUDE_CODE_ENTRYPOINT"] = "sdk-go"
	envMap["CLAUDE_AGENT_SDK_VERSION"] = "0.1.0"
	if t.opts.EnableFileCheckpointing {
		envMap["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
	}
	if t.opts.CWD != "" {
		envMap["PWD"] = t.opts.CWD
	}
	for k, v := range t.opts.Env {
		envMap[k] = v
	}
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	return env
}

func (t *SubprocessTransport) readStdout() {
	reader := bufio.NewReader(t.stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			maxBufferSize := t.effectiveMaxBufferSize()
			if len(line) > maxBufferSize {
				t.msgCh <- transportMessage{Err: fmt.Errorf("json message exceeded maximum buffer size of %d bytes", maxBufferSize)}
				return
			}
			var payload map[string]any
			if unmarshalErr := json.Unmarshal(bytes.TrimSpace(line), &payload); unmarshalErr != nil {
				t.msgCh <- transportMessage{Err: unmarshalErr}
				return
			}
			t.msgCh <- transportMessage{Data: payload}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.msgCh <- transportMessage{Err: err}
			}
			return
		}
	}
}

func (t *SubprocessTransport) effectiveMaxBufferSize() int {
	if t.opts.MaxBufferSize > 0 {
		return t.opts.MaxBufferSize
	}
	return defaultMaxBufferSize
}

func (t *SubprocessTransport) readStderr() {
	reader := bufio.NewScanner(t.stderr)
	for reader.Scan() {
		line := reader.Text()
		t.stderrBuf.WriteString(line)
		t.stderrBuf.WriteByte('\n')
		if t.opts.Stderr != nil {
			t.opts.Stderr(line)
		}
	}
}

func (t *SubprocessTransport) waitProcess() {
	err := t.cmd.Wait()
	if err == nil {
		close(t.msgCh)
		return
	}

	exitCode := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	t.msgCh <- transportMessage{
		Err: &ProcessError{
			ExitCode: exitCode,
			Stderr:   strings.TrimSpace(t.stderrBuf.String()),
		},
	}
	close(t.msgCh)
}
