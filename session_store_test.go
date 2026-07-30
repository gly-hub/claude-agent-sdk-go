package claudeagentsdk

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type appendLoadOnlyStore struct {
	entries map[string][]SessionStoreEntry
}

type flakyMirrorStore struct {
	mu        sync.Mutex
	failures  int
	appendCnt int
}

func (s *flakyMirrorStore) Append(_ SessionKey, _ []SessionStoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendCnt++
	if s.appendCnt <= s.failures {
		return errors.New("transient store failure")
	}
	return nil
}

func (s *flakyMirrorStore) Load(_ SessionKey) ([]SessionStoreEntry, error) { return nil, nil }

type blockingLoadStore struct {
	release chan struct{}
}

type summaryOnlyStore struct{ *InMemorySessionStore }

func (s *summaryOnlyStore) Load(_ SessionKey) ([]SessionStoreEntry, error) {
	return nil, errors.New("Load must not be called for fresh summaries")
}

func (s *blockingLoadStore) Append(_ SessionKey, _ []SessionStoreEntry) error { return nil }

func (s *blockingLoadStore) Load(_ SessionKey) ([]SessionStoreEntry, error) {
	<-s.release
	return nil, nil
}

func (s *appendLoadOnlyStore) Append(key SessionKey, entries []SessionStoreEntry) error {
	storeKey := sessionStoreMapKey(key)
	s.entries[storeKey] = append(s.entries[storeKey], cloneSessionStoreEntries(entries)...)
	return nil
}

func (s *appendLoadOnlyStore) Load(key SessionKey) ([]SessionStoreEntry, error) {
	return cloneSessionStoreEntries(s.entries[sessionStoreMapKey(key)]), nil
}

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
	subkey := filePathToSessionKey("/tmp/claude/projects/proj/550e8400-e29b-41d4-a716-446655440000/subagents/nested/agent-worker.jsonl", "/tmp/claude/projects")
	if subkey == nil || subkey.Subpath != "subagents/nested/agent-worker" {
		t.Fatalf("unexpected subagent key: %#v", subkey)
	}
}

func TestInMemorySessionStoreListSessionSummaries(t *testing.T) {
	store := NewInMemorySessionStore()
	key := SessionKey{ProjectKey: "proj", SessionID: "550e8400-e29b-41d4-a716-446655440000"}
	if err := store.Append(key, []SessionStoreEntry{{"type": "user", "uuid": "11111111-1111-1111-1111-111111111111", "message": map[string]any{"content": "summary prompt"}}}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	summaries, err := store.ListSessionSummaries("proj")
	if err != nil {
		t.Fatalf("ListSessionSummaries() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].SessionID != key.SessionID || summaries[0].Data == nil || summaries[0].Summary != "summary prompt" {
		t.Fatalf("unexpected summaries: %#v", summaries)
	}
}

func TestFoldSessionSummaryMatchesIncrementalPythonRules(t *testing.T) {
	key := SessionKey{ProjectKey: "proj", SessionID: "550e8400-e29b-41d4-a716-446655440000"}
	first := FoldSessionSummary(nil, key, []SessionStoreEntry{
		{"type": "user", "isSidechain": false, "timestamp": "2026-07-30T00:00:00.123Z", "message": map[string]any{"content": "<command-name>/review</command-name>"}},
		{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "ignored"}}}},
		{"type": "user", "message": map[string]any{"content": "real\nprompt"}},
		{"type": "tag", "tag": "keep"},
		{"type": "custom-title", "aiTitle": "AI title"},
	})
	if first == nil {
		t.Fatal("expected summary")
	}
	if first.FirstPrompt != "real prompt" || first.Summary != "AI title" || first.CreatedAt == 0 || first.Tag != "keep" {
		t.Fatalf("unexpected first fold: %#v", first)
	}

	second := FoldSessionSummary(first, key, []SessionStoreEntry{
		{"type": "custom-title", "customTitle": "User title"},
		{"type": "tag", "tag": ""},
		{"type": "assistant", "lastPrompt": "latest request", "gitBranch": "main"},
	})
	if second == nil {
		t.Fatal("expected summary")
	}
	if second.CustomTitle != "User title" || second.Summary != "User title" || second.Tag != "" || second.GitBranch != "main" {
		t.Fatalf("unexpected second fold: %#v", second)
	}
	if got := stringFromAny(second.Data["command_fallback"]); got != "/review" {
		t.Fatalf("command fallback = %q, want /review", got)
	}
}

func TestFoldSessionSummaryTruncatesFirstPromptAtTwoHundredRunes(t *testing.T) {
	key := SessionKey{SessionID: "550e8400-e29b-41d4-a716-446655440000"}
	prompt := strings.Repeat("界", 201)
	summary := FoldSessionSummary(nil, key, []SessionStoreEntry{{"type": "user", "message": map[string]any{"content": prompt}}})
	if summary == nil {
		t.Fatal("expected summary")
	}
	if got, want := summary.FirstPrompt, strings.Repeat("界", 200)+"…"; got != want {
		t.Fatalf("first prompt length/content mismatch: got %q", got)
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

func TestSessionStoreQueryAndMutationHelpers(t *testing.T) {
	store := NewInMemorySessionStore()
	directory := t.TempDir()
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		t.Fatalf("ProjectKeyForDirectory() error = %v", err)
	}
	sessionID := "550e8400-e29b-41d4-a716-446655440000"
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	if err := store.Append(key, []SessionStoreEntry{
		{"type": "user", "uuid": "11111111-1111-1111-1111-111111111111", "sessionId": sessionID, "cwd": directory, "timestamp": "2026-07-30T00:00:00Z", "message": map[string]any{"content": "hello"}},
		{"type": "assistant", "uuid": "22222222-2222-2222-2222-222222222222", "parentUuid": "11111111-1111-1111-1111-111111111111", "sessionId": sessionID, "message": map[string]any{"content": "world"}},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	infos, err := ListSessionsFromStore(store, directory, 0, 0)
	if err != nil || len(infos) != 1 || infos[0].Summary != "hello" {
		t.Fatalf("ListSessionsFromStore() = %#v, %v", infos, err)
	}
	messages, err := GetSessionMessagesFromStore(store, sessionID, directory, 0, 0)
	if err != nil || len(messages) != 2 || messages[1].Type != "assistant" {
		t.Fatalf("GetSessionMessagesFromStore() = %#v, %v", messages, err)
	}
	if err := RenameSessionViaStore(store, sessionID, "Stored session", directory); err != nil {
		t.Fatalf("RenameSessionViaStore() error = %v", err)
	}
	if err := TagSessionViaStore(store, sessionID, "demo", directory); err != nil {
		t.Fatalf("TagSessionViaStore() error = %v", err)
	}
	info, err := GetSessionInfoFromStore(store, sessionID, directory)
	if err != nil || info == nil || info.CustomTitle != "Stored session" || info.Tag != "demo" {
		t.Fatalf("GetSessionInfoFromStore() = %#v, %v", info, err)
	}
	fork, err := ForkSessionViaStore(store, sessionID, directory, "", "")
	if err != nil || !isUUID(fork.SessionID) {
		t.Fatalf("ForkSessionViaStore() = %#v, %v", fork, err)
	}
	if err := DeleteSessionViaStore(store, sessionID, directory); err != nil {
		t.Fatalf("DeleteSessionViaStore() error = %v", err)
	}
	entries, err := store.Load(key)
	if err != nil || len(entries) != 0 {
		t.Fatalf("expected deleted store session, got %#v, %v", entries, err)
	}
}

func TestListSessionsFromStoreUsesFreshSummaryWithoutLoad(t *testing.T) {
	directory := t.TempDir()
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "550e8400-e29b-41d4-a716-446655440000"
	store := &summaryOnlyStore{NewInMemorySessionStore()}
	if err := store.Append(SessionKey{ProjectKey: projectKey, SessionID: sessionID}, []SessionStoreEntry{{"type": "user", "uuid": "11111111-1111-1111-1111-111111111111", "message": map[string]any{"content": "fast path"}}}); err != nil {
		t.Fatal(err)
	}
	infos, err := ListSessionsFromStore(store, directory, 0, 0)
	if err != nil || len(infos) != 1 || infos[0].Summary != "fast path" {
		t.Fatalf("ListSessionsFromStore() = %#v, %v", infos, err)
	}
}

func TestMinimalSessionStoreSupportsResumeButNotContinue(t *testing.T) {
	directory := t.TempDir()
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		t.Fatalf("ProjectKeyForDirectory() error = %v", err)
	}
	sessionID := "550e8400-e29b-41d4-a716-446655440000"
	store := &appendLoadOnlyStore{entries: map[string][]SessionStoreEntry{}}
	if err := store.Append(SessionKey{ProjectKey: projectKey, SessionID: sessionID}, []SessionStoreEntry{{"type": "user", "uuid": "11111111-1111-1111-1111-111111111111"}}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	materialized, err := MaterializeResumeSession(Options{CWD: directory, SessionStore: store, Resume: sessionID})
	if err != nil || materialized == nil {
		t.Fatalf("MaterializeResumeSession() = %#v, %v", materialized, err)
	}
	defer materialized.Cleanup()
	if _, err := MaterializeResumeSession(Options{CWD: directory, SessionStore: store, ContinueConversation: true}); err == nil {
		t.Fatal("expected ContinueConversation to require SessionListStore")
	}
}

func TestTranscriptMirrorRetriesAndReportsFailureWithoutReturningError(t *testing.T) {
	oldBackoff := mirrorAppendBackoff
	mirrorAppendBackoff = []time.Duration{0, 0}
	t.Cleanup(func() { mirrorAppendBackoff = oldBackoff })

	projectsDir := filepath.Join(t.TempDir(), "projects")
	filePath := filepath.Join(projectsDir, "proj", "550e8400-e29b-41d4-a716-446655440000.jsonl")
	store := &flakyMirrorStore{failures: 2}
	batcher := NewTranscriptMirrorBatcher(store, projectsDir, SessionStoreFlushBatched)
	if err := batcher.Enqueue(filePath, []SessionStoreEntry{{"type": "user"}}); err != nil {
		t.Fatal(err)
	}
	if err := batcher.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if store.appendCnt != 3 {
		t.Fatalf("expected 3 append attempts, got %d", store.appendCnt)
	}

	store = &flakyMirrorStore{failures: 3}
	errorsSeen := make(chan error, 1)
	batcher = NewTranscriptMirrorBatcherWithError(store, projectsDir, SessionStoreFlushBatched, func(_ *SessionKey, err error) {
		errorsSeen <- err
	})
	_ = batcher.Enqueue(filePath, []SessionStoreEntry{{"type": "user"}})
	if err := batcher.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case err := <-errorsSeen:
		if !strings.Contains(err.Error(), "transient") {
			t.Fatalf("unexpected mirror error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected non-fatal mirror error callback")
	}
}

func TestMaterializeResumeHonorsExplicitLoadTimeout(t *testing.T) {
	store := &blockingLoadStore{release: make(chan struct{})}
	defer close(store.release)
	_, err := MaterializeResumeSession(Options{
		CWD:              t.TempDir(),
		SessionStore:     store,
		Resume:           "550e8400-e29b-41d4-a716-446655440000",
		LoadTimeoutMS:    5,
		LoadTimeoutMSSet: true,
	})
	if !errors.Is(err, errSessionStoreTimeout) {
		t.Fatalf("expected load timeout, got %v", err)
	}
}
