package claudeagentsdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestBuildCommandAddsSkillDefaults(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		SystemPrompt: "helpful",
		Skills:       []string{"browser"},
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	if !slices.Contains(cmd, "--allowedTools") {
		t.Fatalf("expected allowed tools flag in command: %#v", cmd)
	}
	if !slices.Contains(cmd, "--setting-sources=user,project") {
		t.Fatalf("expected setting sources default in command: %#v", cmd)
	}
}

func TestBuildCommandSkillsAllAddsBareSkillTool(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		Skills: "all",
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	if !slices.Contains(cmd, "--allowedTools") {
		t.Fatalf("expected allowed tools flag in command: %#v", cmd)
	}
	idx := slices.Index(cmd, "--allowedTools")
	if idx < 0 || idx+1 >= len(cmd) || cmd[idx+1] != "Skill" {
		t.Fatalf("expected bare Skill tool in command: %#v", cmd)
	}
	if !slices.Contains(cmd, "--setting-sources=user,project") {
		t.Fatalf("expected skill setting sources default in command: %#v", cmd)
	}
}

func TestBuildCommandSkillsDoNotDuplicateAllowedTools(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		AllowedTools: []string{"Read", "Skill(pdf)"},
		Skills:       []string{"pdf"},
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	idx := slices.Index(cmd, "--allowedTools")
	if idx < 0 || idx+1 >= len(cmd) {
		t.Fatalf("expected allowed tools flag in command: %#v", cmd)
	}
	if cmd[idx+1] != "Read,Skill(pdf)" {
		t.Fatalf("expected deduplicated allowed tools, got %q", cmd[idx+1])
	}
}

func TestBuildCommandPreservesExplicitEmptySettingSources(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		Skills:         "all",
		SettingSources: []string{},
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	if !slices.Contains(cmd, "--setting-sources=") {
		t.Fatalf("expected explicit empty setting sources to be preserved: %#v", cmd)
	}
	if slices.Contains(cmd, "--setting-sources=user,project") {
		t.Fatalf("did not expect default skill setting sources when caller passed empty slice: %#v", cmd)
	}
}

func TestBuildCommandAddsLocalPluginDirs(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		Plugins: []SDKPluginConfig{
			{Type: PluginTypeLocal, Path: "/tmp/plugin-a"},
			{Type: PluginTypeLocal, Path: "/tmp/plugin-b"},
		},
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	assertFlagValue(t, cmd, "--plugin-dir", "/tmp/plugin-a")
	assertFlagValue(t, cmd, "--plugin-dir", "/tmp/plugin-b")
}

func TestBuildCommandSupportsFlagStyleExtraArgs(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		ExtraArgs: map[string]ExtraArgValue{
			"disable-slash-commands": {IsFlag: true},
			"agent":                  {Value: "reviewer"},
		},
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	if !slices.Contains(cmd, "--disable-slash-commands") {
		t.Fatalf("expected boolean extra arg flag in command: %#v", cmd)
	}
	assertFlagValue(t, cmd, "--agent", "reviewer")
}

func TestBuildCommandAddsNewPythonCompatibleFlags(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		TaskBudget: &TaskBudget{Total: 1234},
		OutputFormat: &OutputFormat{
			Type: "json_schema",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			},
		},
		SessionStore: NewInMemorySessionStore(),
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	assertFlagValue(t, cmd, "--task-budget", "1234")
	assertFlagValue(t, cmd, "--json-schema", `{"properties":{"name":{"type":"string"}},"type":"object"}`)
	if !slices.Contains(cmd, "--session-mirror") {
		t.Fatalf("expected session mirror flag in command: %#v", cmd)
	}
}

func TestBuildCommandCanUseToolDefaultsPermissionPromptToolToStdio(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		CanUseTool: func(req ToolPermissionRequest) (PermissionDecision, error) {
			return PermissionDecision{Behavior: "allow"}, nil
		},
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	assertFlagValue(t, cmd, "--permission-prompt-tool", "stdio")
}

func TestValidatePermissionPromptOptionsRejectsConflict(t *testing.T) {
	err := validatePermissionPromptOptions(Options{
		PermissionPromptTool: "custom",
		CanUseTool: func(req ToolPermissionRequest) (PermissionDecision, error) {
			return PermissionDecision{Behavior: "allow"}, nil
		},
	})
	if err == nil {
		t.Fatal("expected CanUseTool and custom PermissionPromptTool conflict")
	}
}

func TestBuildCommandMergesSandboxIntoSettingsJSON(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		Settings: `{"permissions":{"allow":["Bash(ls)"]}}`,
		Sandbox: SandboxSettings{
			"enabled": true,
		},
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	value := flagValue(t, cmd, "--settings")
	var got map[string]any
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("settings is not JSON: %v\n%s", err, value)
	}
	if got["sandbox"] == nil || got["permissions"] == nil {
		t.Fatalf("expected sandbox and existing settings to be present: %#v", got)
	}
}

func TestBuildCommandMergesSandboxIntoSettingsFile(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	if err := os.WriteFile(path, []byte(`{"env":{"A":"B"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	transport := NewSubprocessTransport(Options{
		Settings: path,
		Sandbox:  SandboxSettings{"enabled": true},
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	value := flagValue(t, cmd, "--settings")
	var got map[string]any
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("settings is not JSON: %v\n%s", err, value)
	}
	if got["sandbox"] == nil || got["env"] == nil {
		t.Fatalf("expected sandbox and file settings to be present: %#v", got)
	}
}

func TestBuildCommandSupportsPresets(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		ToolsPreset: ToolsPresetClaudeCode,
		SystemPromptPreset: &SystemPromptPreset{
			Preset: "claude_code",
			Append: "extra",
		},
	})

	cmd := transport.buildCommand("/usr/local/bin/claude")
	assertFlagValue(t, cmd, "--tools", "default")
	assertFlagValue(t, cmd, "--append-system-prompt", "extra")
}

func TestBuildEnvAddsFileCheckpointing(t *testing.T) {
	transport := NewSubprocessTransport(Options{EnableFileCheckpointing: true})
	env := envSliceToMap(transport.buildEnv(context.Background()))
	if env["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] != "true" {
		t.Fatalf("expected checkpointing env, got %#v", env)
	}
}

func TestBuildEnvEnablesFineGrainedToolStreamingForPartialMessages(t *testing.T) {
	transport := NewSubprocessTransport(Options{IncludePartialMessages: true})
	env := envSliceToMap(transport.buildEnv(context.Background()))
	if env["CLAUDE_CODE_ENABLE_FINE_GRAINED_TOOL_STREAMING"] != "1" {
		t.Fatalf("expected fine grained tool streaming env, got %#v", env)
	}
}

func TestBuildEnvMatchesPythonSDKPrecedence(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		Env: map[string]string{
			"CLAUDE_CODE_ENTRYPOINT":   "custom-entrypoint",
			"CLAUDE_AGENT_SDK_VERSION": "user-version",
		},
	})

	env := envSliceToMap(transport.buildEnv(context.Background()))
	if env["CLAUDE_CODE_ENTRYPOINT"] != "custom-entrypoint" {
		t.Fatalf("expected entrypoint to be overridable, got %#v", env["CLAUDE_CODE_ENTRYPOINT"])
	}
	if env["CLAUDE_AGENT_SDK_VERSION"] != sdkVersion {
		t.Fatalf("expected SDK version to be forced to %q, got %#v", sdkVersion, env["CLAUDE_AGENT_SDK_VERSION"])
	}
}

func TestBuildEnvInjectsOTELTraceContext(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(prev)

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		SpanID:     trace.SpanID{2, 2, 2, 2, 2, 2, 2, 2},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	transport := NewSubprocessTransport(Options{})
	env := envSliceToMap(transport.buildEnv(ctx))
	if env["TRACEPARENT"] == "" {
		t.Fatalf("expected TRACEPARENT to be injected, got %#v", env)
	}
}

func TestBuildEnvReplacesInheritedTraceContextWhenActiveSpanExists(t *testing.T) {
	prevProp := otel.GetTextMapPropagator()
	prevTraceparent, hadTraceparent := os.LookupEnv("TRACEPARENT")
	prevTracestate, hadTracestate := os.LookupEnv("TRACESTATE")
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(prevProp)
	defer restoreEnv("TRACEPARENT", prevTraceparent, hadTraceparent)
	defer restoreEnv("TRACESTATE", prevTracestate, hadTracestate)

	_ = os.Setenv("TRACEPARENT", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")
	_ = os.Setenv("TRACESTATE", "vendor=old")

	traceState, err := trace.TraceState{}.Insert("vendor", "new")
	if err != nil {
		t.Fatalf("failed to build TraceState: %v", err)
	}

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3},
		SpanID:     trace.SpanID{4, 4, 4, 4, 4, 4, 4, 4},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
		TraceState: traceState,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	transport := NewSubprocessTransport(Options{})
	env := envSliceToMap(transport.buildEnv(ctx))
	if env["TRACEPARENT"] == "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01" {
		t.Fatalf("expected inherited TRACEPARENT to be replaced, got %#v", env["TRACEPARENT"])
	}
	if env["TRACESTATE"] != "vendor=new" {
		t.Fatalf("expected TRACESTATE to be refreshed, got %#v", env["TRACESTATE"])
	}
}

func TestBuildEnvPreservesExplicitTraceOverrides(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(prev)

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5},
		SpanID:     trace.SpanID{6, 6, 6, 6, 6, 6, 6, 6},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	transport := NewSubprocessTransport(Options{
		Env: map[string]string{
			"TRACEPARENT": "custom-traceparent",
		},
	})
	env := envSliceToMap(transport.buildEnv(ctx))
	if env["TRACEPARENT"] != "custom-traceparent" {
		t.Fatalf("expected caller TRACEPARENT override to win, got %#v", env["TRACEPARENT"])
	}
}

func TestValidateBufferSizeRejectsNegative(t *testing.T) {
	if err := validateBufferSize(-1); err == nil {
		t.Fatal("expected negative buffer size error")
	}
}

func TestEffectiveMaxBufferSizeDefaultsToPythonLimit(t *testing.T) {
	transport := NewSubprocessTransport(Options{})
	if got := transport.effectiveMaxBufferSize(); got != defaultMaxBufferSize {
		t.Fatalf("unexpected default buffer size: %d", got)
	}
}

func TestReadStdoutOwnsMessageChannelClose(t *testing.T) {
	stdout, writer := io.Pipe()
	transport := NewSubprocessTransport(Options{})
	transport.stdout = stdout
	transport.processDone = make(chan struct{})

	go transport.readStdout()
	close(transport.processDone)

	if _, err := writer.Write([]byte(`{"type":"system","subtype":"ready"}` + "\n")); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout: %v", err)
	}

	msg, ok := <-transport.ReadMessages()
	if !ok {
		t.Fatal("expected stdout message before channel close")
	}
	if msg.Data["type"] != "system" || msg.Data["subtype"] != "ready" {
		t.Fatalf("unexpected message: %#v", msg.Data)
	}
	if _, ok := <-transport.ReadMessages(); ok {
		t.Fatal("expected message channel to close after stdout is drained")
	}
}

func TestReadStdoutAccumulatesPartialJSONAndSkipsNoise(t *testing.T) {
	stdout, writer := io.Pipe()
	transport := NewSubprocessTransport(Options{})
	transport.stdout = stdout
	transport.processDone = make(chan struct{})

	go transport.readStdout()
	close(transport.processDone)

	chunks := [][]byte{
		[]byte("[SandboxDebug] ignored\n"),
		[]byte(`{"type":"system",` + "\n"),
		[]byte(`"subtype":"ready"}` + "\n"),
	}
	for _, chunk := range chunks {
		if _, err := writer.Write(chunk); err != nil {
			t.Fatalf("write stdout: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout: %v", err)
	}

	msg, ok := <-transport.ReadMessages()
	if !ok {
		t.Fatal("expected accumulated stdout message")
	}
	if msg.Err != nil {
		t.Fatalf("unexpected read error: %v", msg.Err)
	}
	if msg.Data["type"] != "system" || msg.Data["subtype"] != "ready" {
		t.Fatalf("unexpected message: %#v", msg.Data)
	}
}

func TestVersionHelpers(t *testing.T) {
	if got := extractVersion("claude 2.0.75"); got != "2.0.75" {
		t.Fatalf("unexpected version: %s", got)
	}
	if compareVersions("2.0.75", "2.0.0") <= 0 {
		t.Fatal("expected 2.0.75 to be newer than 2.0.0")
	}
	if compareVersions("1.9.9", "2.0.0") >= 0 {
		t.Fatal("expected 1.9.9 to be older than 2.0.0")
	}
}

func TestDownloadCLIInstallsIntoCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`#!/usr/bin/env bash
set -e
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/claude" <<'SCRIPT'
#!/usr/bin/env bash
echo claude 2.1.179
SCRIPT
chmod +x "$HOME/.local/bin/claude"
`))
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	transport := NewSubprocessTransport(Options{
		AllowCLIDownload: true,
		CLIVersion:       "2.1.179",
		CLICacheDir:      cacheDir,
		CLIDownloadURL:   server.URL,
	})

	path, err := transport.downloadCLI(context.Background())
	if err != nil {
		t.Fatalf("downloadCLI() error = %v", err)
	}
	expected := filepath.Join(cacheDir, "2.1.179", runtime.GOOS+"_"+runtime.GOARCH, "claude")
	if path != expected {
		t.Fatalf("unexpected cached path:\n got: %s\nwant: %s", path, expected)
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		t.Fatalf("expected cached CLI at %s, stat=%#v err=%v", path, info, err)
	}
	if requests != 1 {
		t.Fatalf("expected one installer request, got %d", requests)
	}

	path, err = transport.downloadCLI(context.Background())
	if err != nil {
		t.Fatalf("second downloadCLI() error = %v", err)
	}
	if path != expected {
		t.Fatalf("unexpected cached path on second call: %s", path)
	}
	if requests != 1 {
		t.Fatalf("expected cached CLI to avoid a second request, got %d", requests)
	}
}

func TestDownloadCLIRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	}))
	defer server.Close()

	transport := NewSubprocessTransport(Options{
		AllowCLIDownload: true,
		CLICacheDir:      t.TempDir(),
		CLIDownloadURL:   server.URL,
	})
	if _, err := transport.downloadCLI(context.Background()); err == nil {
		t.Fatal("expected HTTP error from downloadCLI")
	}
}

func TestValidatePluginConfigsRejectsUnsupportedType(t *testing.T) {
	err := validatePluginConfigs([]SDKPluginConfig{{Type: "remote", Path: "/tmp/plugin"}})
	if err == nil {
		t.Fatal("expected unsupported plugin type error")
	}
}

func TestValidatePluginConfigsRejectsEmptyPath(t *testing.T) {
	err := validatePluginConfigs([]SDKPluginConfig{{Type: PluginTypeLocal}})
	if err == nil {
		t.Fatal("expected empty plugin path error")
	}
}

func TestApplyCommandUserEmptyIsNoop(t *testing.T) {
	if err := applyCommandUser(nilSafeCmd(), ""); err != nil {
		t.Fatalf("expected empty user to be a no-op, got %v", err)
	}
}

func assertFlagValue(t *testing.T, cmd []string, flag string, value string) {
	t.Helper()
	for i := 0; i < len(cmd)-1; i++ {
		if cmd[i] == flag && cmd[i+1] == value {
			return
		}
	}
	t.Fatalf("expected %s %s in command: %#v", flag, value, cmd)
}

func flagValue(t *testing.T, cmd []string, flag string) string {
	t.Helper()
	for i := 0; i < len(cmd)-1; i++ {
		if cmd[i] == flag {
			return cmd[i+1]
		}
	}
	t.Fatalf("expected %s in command: %#v", flag, cmd)
	return ""
}

func envSliceToMap(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		for i, ch := range entry {
			if ch == '=' {
				out[entry[:i]] = entry[i+1:]
				break
			}
		}
	}
	return out
}

func nilSafeCmd() *exec.Cmd {
	return &exec.Cmd{}
}

func restoreEnv(key, value string, had bool) {
	if had {
		_ = os.Setenv(key, value)
		return
	}
	_ = os.Unsetenv(key)
}
