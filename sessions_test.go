package claudeagentsdk

import (
	"os"
	"os/exec"
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

func TestListSessionsWithOptionsListsAllProjectsAndPaginates(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	for _, item := range []struct {
		directory string
		sessionID string
		prompt    string
	}{
		{directory: t.TempDir(), sessionID: "11111111-1111-1111-1111-111111111111", prompt: "first"},
		{directory: t.TempDir(), sessionID: "22222222-2222-2222-2222-222222222222", prompt: "second"},
	} {
		projectKey, err := ProjectKeyForDirectory(item.directory)
		if err != nil {
			t.Fatal(err)
		}
		storageDir := filepath.Join(configDir, "projects", projectKey)
		if err := os.MkdirAll(storageDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := `{"type":"user","uuid":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","sessionId":"` + item.sessionID + `","message":{"content":"` + item.prompt + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(storageDir, item.sessionID+".jsonl"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	sessions, err := ListSessionsWithOptions(SessionListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("ListSessionsWithOptions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one paginated global session, got %#v", sessions)
	}
	all, err := ListSessionsWithOptions(SessionListOptions{})
	if err != nil || len(all) != 2 {
		t.Fatalf("expected two global sessions, got %#v, %v", all, err)
	}
	page, err := ListSessionsWithOptions(SessionListOptions{Offset: 1, Limit: 1})
	if err != nil || len(page) != 1 || page[0].SessionID == sessions[0].SessionID {
		t.Fatalf("unexpected paginated global sessions: first=%#v page=%#v err=%v", sessions, page, err)
	}
}

func TestListSessionsWithOptionsIncludesGitWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for worktree discovery")
	}
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	repository := filepath.Join(t.TempDir(), "repository")
	runGit(t, "init", repository)
	runGit(t, "-C", repository, "config", "user.email", "sdk@example.test")
	runGit(t, "-C", repository, "config", "user.name", "SDK Test")
	runGit(t, "-C", repository, "commit", "--allow-empty", "-m", "initial")
	worktree := filepath.Join(t.TempDir(), "worktree")
	runGit(t, "-C", repository, "worktree", "add", "-b", "sdk-worktree", worktree)

	sessionID := "33333333-3333-3333-3333-333333333333"
	projectKey, err := ProjectKeyForDirectory(worktree)
	if err != nil {
		t.Fatal(err)
	}
	storageDir := filepath.Join(configDir, "projects", projectKey)
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"user","uuid":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","sessionId":"` + sessionID + `","message":{"content":"worktree prompt"}}` + "\n"
	if err := os.WriteFile(filepath.Join(storageDir, sessionID+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	withoutWorktrees, err := ListSessionsWithOptions(SessionListOptions{Directory: repository})
	if err != nil || len(withoutWorktrees) != 0 {
		t.Fatalf("expected no worktree sessions when disabled, got %#v, %v", withoutWorktrees, err)
	}
	withWorktrees, err := ListSessionsWithOptions(SessionListOptions{Directory: repository, IncludeWorktrees: true})
	if err != nil || len(withWorktrees) != 1 || withWorktrees[0].SessionID != sessionID {
		t.Fatalf("expected worktree session, got %#v, %v", withWorktrees, err)
	}
}

func runGit(t *testing.T, args ...string) {
	t.Helper()
	if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func TestSubagentSessionMessages(t *testing.T) {
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
	agentID := "worker-1"
	if err := os.WriteFile(filepath.Join(storageDir, sessionID+".jsonl"), []byte(`{"type":"user"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	subagentDir := filepath.Join(storageDir, sessionID, "subagents", "workflows", "run-1")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := strings.Join([]string{
		`{"type":"user","uuid":"11111111-1111-1111-1111-111111111111","sessionId":"550e8400-e29b-41d4-a716-446655440000","message":{"content":"delegate"}}`,
		`{"type":"assistant","uuid":"22222222-2222-2222-2222-222222222222","parentUuid":"11111111-1111-1111-1111-111111111111","sessionId":"550e8400-e29b-41d4-a716-446655440000","message":{"content":"done"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(subagentDir, "agent-"+agentID+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	ids, err := ListSubagents(sessionID, projectDir)
	if err != nil || len(ids) != 1 || ids[0] != agentID {
		t.Fatalf("ListSubagents() = %#v, %v", ids, err)
	}
	messages, err := GetSubagentMessages(sessionID, agentID, projectDir, 0, 0)
	if err != nil || len(messages) != 2 || messages[1].Type != "assistant" {
		t.Fatalf("GetSubagentMessages() = %#v, %v", messages, err)
	}
}

func TestGetSessionMessagesReturnsVisibleConversationChain(t *testing.T) {
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
		`{"type":"user","uuid":"11111111-1111-1111-1111-111111111111","sessionId":"550e8400-e29b-41d4-a716-446655440000","message":{"content":"root"}}`,
		`{"type":"assistant","uuid":"22222222-2222-2222-2222-222222222222","parentUuid":"11111111-1111-1111-1111-111111111111","sessionId":"550e8400-e29b-41d4-a716-446655440000","message":{"content":"first"}}`,
		`{"type":"user","uuid":"33333333-3333-3333-3333-333333333333","parentUuid":"22222222-2222-2222-2222-222222222222","sessionId":"550e8400-e29b-41d4-a716-446655440000","isSidechain":true,"message":{"content":"sidechain"}}`,
		`{"type":"assistant","uuid":"44444444-4444-4444-4444-444444444444","parentUuid":"33333333-3333-3333-3333-333333333333","sessionId":"550e8400-e29b-41d4-a716-446655440000","isSidechain":true,"message":{"content":"ignored"}}`,
		`{"type":"user","uuid":"55555555-5555-5555-5555-555555555555","parentUuid":"22222222-2222-2222-2222-222222222222","sessionId":"550e8400-e29b-41d4-a716-446655440000","message":{"content":"continue"}}`,
		`{"type":"assistant","uuid":"66666666-6666-6666-6666-666666666666","parentUuid":"55555555-5555-5555-5555-555555555555","sessionId":"550e8400-e29b-41d4-a716-446655440000","message":{"content":"final"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(storageDir, sessionID+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	messages, err := GetSessionMessages(sessionID, projectDir, 2, 1)
	if err != nil {
		t.Fatalf("GetSessionMessages() error = %v", err)
	}
	if len(messages) != 2 || messages[0].UUID != "22222222-2222-2222-2222-222222222222" || messages[1].UUID != "55555555-5555-5555-5555-555555555555" {
		t.Fatalf("unexpected visible conversation page: %#v", messages)
	}
}

func TestImportSessionToStoreIncludesSubagents(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(storageDir, sessionID+".jsonl"), []byte(`{"type":"user","uuid":"11111111-1111-1111-1111-111111111111"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	subagentDir := filepath.Join(storageDir, sessionID, "subagents")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	subagentPath := filepath.Join(subagentDir, "agent-worker.jsonl")
	if err := os.WriteFile(subagentPath, []byte(`{"type":"assistant","uuid":"22222222-2222-2222-2222-222222222222"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(strings.TrimSuffix(subagentPath, ".jsonl")+".meta.json", []byte(`{"agentType":"worker"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(store, sessionID, projectDir, true, 1); err != nil {
		t.Fatalf("ImportSessionToStore() error = %v", err)
	}
	main, _ := store.Load(SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	subagent, _ := store.Load(SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: "subagents/agent-worker"})
	if len(main) != 1 || len(subagent) != 2 || stringFromAny(subagent[1]["type"]) != "agent_metadata" {
		t.Fatalf("unexpected imported entries: main=%#v subagent=%#v", main, subagent)
	}
}
