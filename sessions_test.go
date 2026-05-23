package claudeagentsdk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectKeyForDirectory(t *testing.T) {
	dir := t.TempDir()
	key, err := ProjectKeyForDirectory(dir)
	if err != nil {
		t.Fatalf("ProjectKeyForDirectory() error = %v", err)
	}
	if key == "" {
		t.Fatalf("expected non-empty key")
	}
}

func TestSessionLifecycle(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	projectDir := t.TempDir()
	projectKey, err := ProjectKeyForDirectory(projectDir)
	if err != nil {
		t.Fatalf("ProjectKeyForDirectory() error = %v", err)
	}
	storageDir := filepath.Join(configDir, "projects", projectKey)
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	sessionID := "550e8400-e29b-41d4-a716-446655440000"
	content := strings.Join([]string{
		`{"type":"user","uuid":"11111111-1111-1111-1111-111111111111","sessionId":"550e8400-e29b-41d4-a716-446655440000","message":{"role":"user","content":"hello world"},"timestamp":"2026-05-22T10:00:00Z","cwd":"` + projectDir + `"}`,
		`{"type":"assistant","uuid":"22222222-2222-2222-2222-222222222222","sessionId":"550e8400-e29b-41d4-a716-446655440000","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]},"timestamp":"2026-05-22T10:00:01Z","parentUuid":"11111111-1111-1111-1111-111111111111"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(storageDir, sessionID+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	sessions, err := ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Summary != "hello world" {
		t.Fatalf("unexpected summary: %#v", sessions[0])
	}

	messages, err := GetSessionMessages(sessionID, projectDir, 0, 0)
	if err != nil {
		t.Fatalf("GetSessionMessages() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	if err := RenameSession(sessionID, "Renamed Session", projectDir); err != nil {
		t.Fatalf("RenameSession() error = %v", err)
	}
	if err := TagSession(sessionID, "demo", projectDir); err != nil {
		t.Fatalf("TagSession() error = %v", err)
	}

	info, err := GetSessionInfo(sessionID, projectDir)
	if err != nil {
		t.Fatalf("GetSessionInfo() error = %v", err)
	}
	if info == nil || info.CustomTitle != "Renamed Session" {
		t.Fatalf("unexpected info after rename: %#v", info)
	}
	if info.Tag != "demo" {
		t.Fatalf("unexpected tag after tag update: %#v", info)
	}

	fork, err := ForkSession(sessionID, projectDir, "", "")
	if err != nil {
		t.Fatalf("ForkSession() error = %v", err)
	}
	if !isUUID(fork.SessionID) {
		t.Fatalf("fork session id is not a UUID: %s", fork.SessionID)
	}

	if err := DeleteSession(sessionID, projectDir); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
}
