package claudeagentsdk

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
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

func TestBuildCommandEnableAllSkillsAddsBareSkillTool(t *testing.T) {
	transport := NewSubprocessTransport(Options{
		EnableAllSkills: true,
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
	env := envSliceToMap(transport.buildEnv())
	if env["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] != "true" {
		t.Fatalf("expected checkpointing env, got %#v", env)
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
