package claudeagentsdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type InMemorySessionStore struct {
	mu      sync.Mutex
	entries map[string][]SessionStoreEntry
	mtimes  map[string]int64
}

func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		entries: map[string][]SessionStoreEntry{},
		mtimes:  map[string]int64{},
	}
}

func (s *InMemorySessionStore) Append(key SessionKey, entries []SessionStoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	storeKey := sessionStoreMapKey(key)
	for _, entry := range entries {
		s.entries[storeKey] = append(s.entries[storeKey], cloneSessionStoreEntry(entry))
	}
	s.mtimes[storeKey] = time.Now().UnixMilli()
	return nil
}

func (s *InMemorySessionStore) Load(key SessionKey) ([]SessionStoreEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	storeKey := sessionStoreMapKey(key)
	items, ok := s.entries[storeKey]
	if !ok {
		return nil, nil
	}
	out := make([]SessionStoreEntry, 0, len(items))
	for _, entry := range items {
		out = append(out, cloneSessionStoreEntry(entry))
	}
	return out, nil
}

func (s *InMemorySessionStore) ListSessions(projectKey string) ([]SessionStoreListEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]SessionStoreListEntry, 0)
	for key, mtime := range s.mtimes {
		parsed := parseSessionStoreMapKey(key)
		if parsed.ProjectKey != projectKey || parsed.Subpath != "" {
			continue
		}
		list = append(list, SessionStoreListEntry{
			SessionID: parsed.SessionID,
			MTime:     mtime,
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].MTime > list[j].MTime })
	return list, nil
}

func (s *InMemorySessionStore) ListSessionSummaries(projectKey string) ([]SessionStoreSummary, error) {
	sessions, err := s.ListSessions(projectKey)
	if err != nil {
		return nil, err
	}
	summaries := make([]SessionStoreSummary, 0, len(sessions))
	for _, session := range sessions {
		summaries = append(summaries, SessionStoreSummary{
			SessionID:    session.SessionID,
			LastModified: session.MTime,
		})
	}
	return summaries, nil
}

func (s *InMemorySessionStore) ListSubkeys(key SessionListSubkeysKey) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subkeys := make([]string, 0)
	for raw := range s.entries {
		parsed := parseSessionStoreMapKey(raw)
		if parsed.ProjectKey == key.ProjectKey && parsed.SessionID == key.SessionID && parsed.Subpath != "" {
			subkeys = append(subkeys, parsed.Subpath)
		}
	}
	sort.Strings(subkeys)
	return subkeys, nil
}

func (s *InMemorySessionStore) Delete(key SessionKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	storeKey := sessionStoreMapKey(key)
	delete(s.entries, storeKey)
	delete(s.mtimes, storeKey)
	if key.Subpath == "" {
		prefix := key.ProjectKey + "\x00" + key.SessionID + "\x00"
		for raw := range s.entries {
			if strings.HasPrefix(raw, prefix) {
				delete(s.entries, raw)
				delete(s.mtimes, raw)
			}
		}
	}
	return nil
}

type MaterializedResume struct {
	ConfigDir       string
	ResumeSessionID string
	cleanup         func() error
}

func (m *MaterializedResume) Cleanup() error {
	if m == nil || m.cleanup == nil {
		return nil
	}
	return m.cleanup()
}

func MaterializeResumeSession(opts Options) (*MaterializedResume, error) {
	if opts.SessionStore == nil {
		return nil, nil
	}
	if opts.Resume == "" && !opts.ContinueConversation {
		return nil, nil
	}

	timeout := time.Duration(opts.LoadTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	projectKey, err := ProjectKeyForDirectory(opts.CWD)
	if err != nil {
		return nil, err
	}

	var sessionID string
	var entries []SessionStoreEntry
	if opts.Resume != "" {
		if !isUUID(opts.Resume) {
			return nil, nil
		}
		sessionID, entries, err = loadStoreCandidate(opts.SessionStore, projectKey, opts.Resume, timeout)
	} else {
		sessionID, entries, err = resolveContinueCandidate(opts.SessionStore, projectKey, timeout)
	}
	if err != nil {
		return nil, err
	}
	if sessionID == "" || len(entries) == 0 {
		return nil, nil
	}

	tmpBase, err := os.MkdirTemp("", "claude-resume-")
	if err != nil {
		return nil, err
	}
	cleanup := func() error { return os.RemoveAll(tmpBase) }

	projectDir := filepath.Join(tmpBase, "projects", projectKey)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		_ = cleanup()
		return nil, err
	}
	if err := writeJSONL(filepath.Join(projectDir, sessionID+".jsonl"), entries); err != nil {
		_ = cleanup()
		return nil, err
	}
	_ = copyAuthFiles(tmpBase, opts.Env)

	subkeys, err := opts.SessionStore.ListSubkeys(SessionListSubkeysKey{ProjectKey: projectKey, SessionID: sessionID})
	if err == nil {
		for _, subpath := range subkeys {
			if subpath == "" || strings.Contains(subpath, "..") || filepath.IsAbs(subpath) {
				continue
			}
			subEntries, loadErr := opts.SessionStore.Load(SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath})
			if loadErr != nil || len(subEntries) == 0 {
				continue
			}
			subFile := filepath.Join(projectDir, sessionID, subpath+".jsonl")
			if err := writeJSONL(subFile, subEntries); err != nil {
				_ = cleanup()
				return nil, err
			}
		}
	}

	return &MaterializedResume{
		ConfigDir:       tmpBase,
		ResumeSessionID: sessionID,
		cleanup:         cleanup,
	}, nil
}

type TranscriptMirrorBatcher struct {
	store             SessionStore
	projectsDir       string
	maxPendingEntries int
	maxPendingBytes   int
	mu                sync.Mutex
	pending           []mirrorEntry
	pendingEntries    int
	pendingBytes      int
}

type mirrorEntry struct {
	filePath string
	entries  []SessionStoreEntry
	size     int
}

func NewTranscriptMirrorBatcher(store SessionStore, projectsDir string, mode SessionStoreFlushMode) *TranscriptMirrorBatcher {
	b := &TranscriptMirrorBatcher{
		store:             store,
		projectsDir:       projectsDir,
		maxPendingEntries: 500,
		maxPendingBytes:   1 << 20,
	}
	if mode == SessionStoreFlushEager {
		b.maxPendingEntries = 0
		b.maxPendingBytes = 0
	}
	return b
}

func (b *TranscriptMirrorBatcher) Enqueue(filePath string, entries []SessionStoreEntry) error {
	if b == nil || b.store == nil || len(entries) == 0 {
		return nil
	}
	raw, _ := json.Marshal(entries)
	b.mu.Lock()
	b.pending = append(b.pending, mirrorEntry{filePath: filePath, entries: cloneSessionStoreEntries(entries), size: len(raw)})
	b.pendingEntries += len(entries)
	b.pendingBytes += len(raw)
	shouldFlush := b.pendingEntries > b.maxPendingEntries || b.pendingBytes > b.maxPendingBytes
	b.mu.Unlock()
	if shouldFlush {
		return b.Flush()
	}
	return nil
}

func (b *TranscriptMirrorBatcher) Flush() error {
	if b == nil || b.store == nil {
		return nil
	}
	b.mu.Lock()
	items := b.pending
	b.pending = nil
	b.pendingEntries = 0
	b.pendingBytes = 0
	b.mu.Unlock()
	if len(items) == 0 {
		return nil
	}
	grouped := map[string][]SessionStoreEntry{}
	for _, item := range items {
		grouped[item.filePath] = append(grouped[item.filePath], item.entries...)
	}
	for filePath, entries := range grouped {
		key := filePathToSessionKey(filePath, b.projectsDir)
		if key == nil {
			continue
		}
		if err := b.store.Append(*key, entries); err != nil {
			return err
		}
	}
	return nil
}

func filePathToSessionKey(filePath string, projectsDir string) *SessionKey {
	rel, err := filepath.Rel(projectsDir, filePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 {
		return nil
	}
	projectKey := parts[0]
	if strings.HasSuffix(parts[1], ".jsonl") {
		sessionID := strings.TrimSuffix(parts[1], ".jsonl")
		if !isUUID(sessionID) {
			return nil
		}
		return &SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	}
	if len(parts) >= 3 && parts[1] != "" {
		sessionID := parts[1]
		if !isUUID(sessionID) {
			return nil
		}
		subpath := strings.TrimSuffix(filepath.Join(parts[2:]...), ".jsonl")
		return &SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath}
	}
	return nil
}

func loadStoreCandidate(store SessionStore, projectKey string, sessionID string, _ time.Duration) (string, []SessionStoreEntry, error) {
	entries, err := store.Load(SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	if err != nil || len(entries) == 0 {
		return "", nil, err
	}
	return sessionID, entries, nil
}

func resolveContinueCandidate(store SessionStore, projectKey string, timeout time.Duration) (string, []SessionStoreEntry, error) {
	list, err := store.ListSessions(projectKey)
	if err != nil {
		return "", nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].MTime > list[j].MTime })
	for _, item := range list {
		if !isUUID(item.SessionID) {
			continue
		}
		sessionID, entries, loadErr := loadStoreCandidate(store, projectKey, item.SessionID, timeout)
		if loadErr != nil || len(entries) == 0 {
			continue
		}
		if first := entries[0]; boolFromAny(first["isSidechain"]) {
			continue
		}
		return sessionID, entries, nil
	}
	return "", nil, nil
}

func writeJSONL(path string, entries []SessionStoreEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, entry := range entries {
		body, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(body, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func copyAuthFiles(tmpBase string, env map[string]string) error {
	configDir := env["CLAUDE_CONFIG_DIR"]
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".claude")
	}
	copyIfPresent(filepath.Join(configDir, ".credentials.json"), filepath.Join(tmpBase, ".credentials.json"))
	claudeJSON := filepath.Join(filepath.Dir(configDir), ".claude.json")
	if env["CLAUDE_CONFIG_DIR"] != "" {
		claudeJSON = filepath.Join(configDir, ".claude.json")
	}
	copyIfPresent(claudeJSON, filepath.Join(tmpBase, ".claude.json"))
	return nil
}

func copyIfPresent(src string, dst string) {
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = os.WriteFile(dst, data, 0o600)
}

func cloneSessionStoreEntries(entries []SessionStoreEntry) []SessionStoreEntry {
	out := make([]SessionStoreEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, cloneSessionStoreEntry(entry))
	}
	return out
}

func cloneSessionStoreEntry(entry SessionStoreEntry) SessionStoreEntry {
	raw, _ := json.Marshal(entry)
	var out SessionStoreEntry
	_ = json.Unmarshal(raw, &out)
	return out
}

func sessionStoreMapKey(key SessionKey) string {
	return key.ProjectKey + "\x00" + key.SessionID + "\x00" + key.Subpath
}

func parseSessionStoreMapKey(raw string) SessionKey {
	parts := strings.SplitN(raw, "\x00", 3)
	key := SessionKey{}
	if len(parts) > 0 {
		key.ProjectKey = parts[0]
	}
	if len(parts) > 1 {
		key.SessionID = parts[1]
	}
	if len(parts) > 2 {
		key.Subpath = parts[2]
	}
	return key
}

func validateSessionStoreOptions(opts Options) error {
	if opts.SessionStore == nil {
		return nil
	}
	if opts.Resume == "" && !opts.ContinueConversation && opts.SessionStoreFlush == "" {
		return nil
	}
	if opts.LoadTimeoutMS < 0 {
		return fmt.Errorf("load timeout must be non-negative")
	}
	return nil
}

var errNoSessionFilePathKey = errors.New("session file path could not be mapped to session key")
