package claudeagentsdk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInMemorySessionStoreAndMaterializeResume(t *testing.T) {
	store := NewInMemorySessionStore()
	key := SessionKey{ProjectKey: "proj", SessionID: "550e8400-e29b-41d4-a716-446655440000"}
	err := store.Append(key, []SessionStoreEntry{
		{
			"type":      "user",
			"uuid":      "11111111-1111-1111-1111-111111111111",
			"sessionId": key.SessionID,
			"message":   map[string]any{"role": "user", "content": "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	storeEntries, err := store.Load(key)
	if err != nil || len(storeEntries) != 1 {
		t.Fatalf("Load() = %v, %v", storeEntries, err)
	}

	tmpProject := t.TempDir()
	opts := Options{
		CWD:           tmpProject,
		SessionStore:  store,
		Resume:        key.SessionID,
		LoadTimeoutMS: 1000,
	}

	projectKey, err := ProjectKeyForDirectory(tmpProject)
	if err != nil {
		t.Fatalf("ProjectKeyForDirectory() error = %v", err)
	}
	if projectKey != "proj" {
		_ = store.Delete(key)
		_ = store.Append(SessionKey{ProjectKey: projectKey, SessionID: key.SessionID}, storeEntries)
		opts.Resume = key.SessionID
	}

	materialized, err := MaterializeResumeSession(opts)
	if err != nil {
		t.Fatalf("MaterializeResumeSession() error = %v", err)
	}
	if materialized == nil {
		t.Fatalf("expected materialized resume")
	}
	defer materialized.Cleanup()

	path := filepath.Join(materialized.ConfigDir, "projects", projectKey, key.SessionID+".jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected materialized transcript at %s: %v", path, err)
	}
}

func TestFilePathToSessionKey(t *testing.T) {
	key := filePathToSessionKey("/tmp/claude/projects/proj/550e8400-e29b-41d4-a716-446655440000.jsonl", "/tmp/claude/projects")
	if key == nil || key.ProjectKey != "proj" || key.SessionID == "" {
		t.Fatalf("unexpected key: %#v", key)
	}
}

func TestInMemorySessionStoreListSessionSummaries(t *testing.T) {
	store := NewInMemorySessionStore()
	key := SessionKey{ProjectKey: "proj", SessionID: "550e8400-e29b-41d4-a716-446655440000"}
	if err := store.Append(key, []SessionStoreEntry{{"type": "user"}}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	summaries, err := store.ListSessionSummaries("proj")
	if err != nil {
		t.Fatalf("ListSessionSummaries() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].SessionID != key.SessionID {
		t.Fatalf("unexpected summaries: %#v", summaries)
	}
}

func TestSubagentSessionMessagesFromStore(t *testing.T) {
	store := NewInMemorySessionStore()
	directory := t.TempDir()
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		t.Fatalf("ProjectKeyForDirectory() error = %v", err)
	}
	sessionID := "550e8400-e29b-41d4-a716-446655440000"
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: "subagents/workflows/run-1/agent-worker-1"}
	if err := store.Append(key, []SessionStoreEntry{
		{"type": "agent_metadata"},
		{"type": "user", "uuid": "11111111-1111-1111-1111-111111111111", "sessionId": sessionID, "message": map[string]any{"content": "delegate"}},
		{"type": "assistant", "uuid": "22222222-2222-2222-2222-222222222222", "parentUuid": "11111111-1111-1111-1111-111111111111", "sessionId": sessionID, "message": map[string]any{"content": "done"}},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	ids, err := ListSubagentsFromStore(store, sessionID, directory)
	if err != nil || len(ids) != 1 || ids[0] != "worker-1" {
		t.Fatalf("ListSubagentsFromStore() = %#v, %v", ids, err)
	}
	messages, err := GetSubagentMessagesFromStore(store, sessionID, "worker-1", directory, 0, 0)
	if err != nil || len(messages) != 2 || messages[0].Type != "user" {
		t.Fatalf("GetSubagentMessagesFromStore() = %#v, %v", messages, err)
	}
}
