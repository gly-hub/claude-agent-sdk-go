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
	"unicode"
	"unicode/utf8"
)

type InMemorySessionStore struct {
	mu        sync.Mutex
	entries   map[string][]SessionStoreEntry
	mtimes    map[string]int64
	summaries map[string]SessionStoreSummary
	lastMTime int64
}

func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		entries:   map[string][]SessionStoreEntry{},
		mtimes:    map[string]int64{},
		summaries: map[string]SessionStoreSummary{},
	}
}

func (s *InMemorySessionStore) Append(key SessionKey, entries []SessionStoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	storeKey := sessionStoreMapKey(key)
	for _, entry := range entries {
		s.entries[storeKey] = append(s.entries[storeKey], cloneSessionStoreEntry(entry))
	}
	mtime := time.Now().UnixMilli()
	if mtime <= s.lastMTime {
		mtime = s.lastMTime + 1
	}
	s.lastMTime = mtime
	s.mtimes[storeKey] = mtime
	if key.Subpath == "" {
		previous := s.summaries[storeKey]
		summary := FoldSessionSummary(&previous, key, entries)
		if summary != nil {
			summary.LastModified = mtime
			s.summaries[storeKey] = *summary
		}
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	summaries := make([]SessionStoreSummary, 0)
	for rawKey := range s.mtimes {
		key := parseSessionStoreMapKey(rawKey)
		if key.ProjectKey != projectKey || key.Subpath != "" {
			continue
		}
		if summary, ok := s.summaries[rawKey]; ok {
			summaries = append(summaries, cloneSessionStoreSummary(summary))
		}
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].LastModified > summaries[j].LastModified })
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
	delete(s.summaries, storeKey)
	if key.Subpath == "" {
		prefix := key.ProjectKey + "\x00" + key.SessionID + "\x00"
		for raw := range s.entries {
			if strings.HasPrefix(raw, prefix) {
				delete(s.entries, raw)
				delete(s.mtimes, raw)
				delete(s.summaries, raw)
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

	timeout := effectiveLoadTimeout(opts)
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

	if subkeyStore, ok := opts.SessionStore.(SessionSubkeyStore); ok {
		subkeys, err := listSubkeysWithTimeout(subkeyStore, SessionListSubkeysKey{ProjectKey: projectKey, SessionID: sessionID}, timeout)
		if err != nil {
			_ = cleanup()
			return nil, err
		}
		for _, subpath := range subkeys {
			if subpath == "" || strings.Contains(subpath, "..") || filepath.IsAbs(subpath) {
				continue
			}
			subEntries, loadErr := loadWithTimeout(opts.SessionStore, SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath}, timeout)
			if loadErr != nil {
				_ = cleanup()
				return nil, loadErr
			}
			if len(subEntries) == 0 {
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
	onError           func(*SessionKey, error)
	maxPendingEntries int
	maxPendingBytes   int
	mu                sync.Mutex
	flushMu           sync.Mutex
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
	return NewTranscriptMirrorBatcherWithError(store, projectsDir, mode, nil)
}

// NewTranscriptMirrorBatcherWithError reports mirror failures without stopping the query.
func NewTranscriptMirrorBatcherWithError(store SessionStore, projectsDir string, mode SessionStoreFlushMode, onError func(*SessionKey, error)) *TranscriptMirrorBatcher {
	b := &TranscriptMirrorBatcher{
		store:             store,
		projectsDir:       projectsDir,
		onError:           onError,
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
		go func() { _ = b.Flush() }()
	}
	return nil
}

func (b *TranscriptMirrorBatcher) Flush() error {
	if b == nil || b.store == nil {
		return nil
	}
	b.flushMu.Lock()
	defer b.flushMu.Unlock()
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
		if err := appendMirrorEntries(b.store, *key, entries); err != nil {
			if b.onError != nil {
				b.onError(key, err)
			}
		}
	}
	return nil
}

const (
	mirrorAppendTimeout     = 60 * time.Second
	mirrorAppendMaxAttempts = 3
)

var mirrorAppendBackoff = []time.Duration{200 * time.Millisecond, 800 * time.Millisecond}

func appendMirrorEntries(store SessionStore, key SessionKey, entries []SessionStoreEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < mirrorAppendMaxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(mirrorAppendBackoff[attempt-1])
		}
		err := appendWithTimeout(store, key, entries, mirrorAppendTimeout)
		if err == nil {
			return nil
		}
		lastErr = err
		if errors.Is(err, errSessionStoreTimeout) {
			break
		}
	}
	return lastErr
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
	if len(parts) == 2 && strings.HasSuffix(parts[1], ".jsonl") {
		sessionID := strings.TrimSuffix(parts[1], ".jsonl")
		return &SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	}
	if len(parts) >= 4 && parts[1] != "" {
		sessionID := parts[1]
		subpath := strings.TrimSuffix(filepath.ToSlash(filepath.Join(parts[2:]...)), ".jsonl")
		return &SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath}
	}
	return nil
}

func loadStoreCandidate(store SessionStore, projectKey string, sessionID string, timeout time.Duration) (string, []SessionStoreEntry, error) {
	entries, err := loadWithTimeout(store, SessionKey{ProjectKey: projectKey, SessionID: sessionID}, timeout)
	if err != nil || len(entries) == 0 {
		return "", nil, err
	}
	return sessionID, entries, nil
}

func resolveContinueCandidate(store SessionStore, projectKey string, timeout time.Duration) (string, []SessionStoreEntry, error) {
	listStore, ok := store.(SessionListStore)
	if !ok {
		return "", nil, fmt.Errorf("session store does not implement ListSessions; ContinueConversation requires SessionListStore")
	}
	list, err := listSessionsWithTimeout(listStore, projectKey, timeout)
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

var errSessionStoreTimeout = errors.New("session store operation timed out")

func effectiveLoadTimeout(opts Options) time.Duration {
	if opts.LoadTimeoutMSSet {
		return time.Duration(opts.LoadTimeoutMS) * time.Millisecond
	}
	if opts.LoadTimeoutMS > 0 {
		return time.Duration(opts.LoadTimeoutMS) * time.Millisecond
	}
	return 60 * time.Second
}

func loadWithTimeout(store SessionStore, key SessionKey, timeout time.Duration) ([]SessionStoreEntry, error) {
	return awaitSessionStore(timeout, "load", func() ([]SessionStoreEntry, error) {
		return store.Load(key)
	})
}

func listSessionsWithTimeout(store SessionListStore, projectKey string, timeout time.Duration) ([]SessionStoreListEntry, error) {
	return awaitSessionStore(timeout, "list sessions", func() ([]SessionStoreListEntry, error) {
		return store.ListSessions(projectKey)
	})
}

func listSubkeysWithTimeout(store SessionSubkeyStore, key SessionListSubkeysKey, timeout time.Duration) ([]string, error) {
	return awaitSessionStore(timeout, "list subkeys", func() ([]string, error) {
		return store.ListSubkeys(key)
	})
}

func appendWithTimeout(store SessionStore, key SessionKey, entries []SessionStoreEntry, timeout time.Duration) error {
	_, err := awaitSessionStore(timeout, "append", func() (struct{}, error) {
		return struct{}{}, store.Append(key, entries)
	})
	return err
}

func awaitSessionStore[T any](timeout time.Duration, operation string, call func() (T, error)) (T, error) {
	var zero T
	if timeout <= 0 {
		return zero, fmt.Errorf("%w: %s", errSessionStoreTimeout, operation)
	}
	type result struct {
		value T
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		value, err := call()
		resultCh <- result{value: value, err: err}
	}()
	select {
	case result := <-resultCh:
		return result.value, result.err
	case <-time.After(timeout):
		return zero, fmt.Errorf("%w after %s: %s", errSessionStoreTimeout, timeout, operation)
	}
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

func cloneSessionStoreSummary(summary SessionStoreSummary) SessionStoreSummary {
	clone := summary
	if summary.Data != nil {
		raw, _ := json.Marshal(summary.Data)
		_ = json.Unmarshal(raw, &clone.Data)
	}
	return clone
}

// FoldSessionSummary updates the SDK-owned summary sidecar for a main transcript.
// Stores should persist Data verbatim and treat it as opaque.
func FoldSessionSummary(previous *SessionStoreSummary, key SessionKey, entries []SessionStoreEntry) *SessionStoreSummary {
	if key.Subpath != "" {
		return nil
	}
	data := map[string]any{}
	if previous != nil && previous.Data != nil {
		raw, _ := json.Marshal(previous.Data)
		_ = json.Unmarshal(raw, &data)
	}
	for _, entry := range entries {
		if _, exists := data["is_sidechain"]; !exists {
			data["is_sidechain"] = boolFromAny(entry["isSidechain"])
		}
		if _, exists := data["created_at"]; !exists {
			if createdAt := parseCreatedAt(stringFromAny(entry["timestamp"])); createdAt != 0 {
				data["created_at"] = createdAt
			}
		}
		if _, exists := data["cwd"]; !exists {
			if value := stringFromAny(entry["cwd"]); value != "" {
				data["cwd"] = value
			}
		}
		foldSessionSummaryFirstPrompt(data, entry)
		for source, destination := range map[string]string{
			"customTitle": "custom_title",
			"aiTitle":     "ai_title",
			"lastPrompt":  "last_prompt",
			"summary":     "summary_hint",
			"gitBranch":   "git_branch",
		} {
			if value, ok := entry[source].(string); ok {
				data[destination] = value
			}
		}
		if stringFromAny(entry["type"]) == "tag" {
			if tag := stringFromAny(entry["tag"]); tag != "" {
				data["tag"] = tag
			} else {
				delete(data, "tag")
			}
		}
	}
	firstPrompt := stringFromAny(data["first_prompt"])
	if !boolFromAny(data["first_prompt_locked"]) {
		firstPrompt = stringFromAny(data["command_fallback"])
	}
	customTitle := firstNonEmpty(stringFromAny(data["custom_title"]), stringFromAny(data["ai_title"]))
	summary := firstNonEmpty(customTitle, stringFromAny(data["last_prompt"]), stringFromAny(data["summary_hint"]), firstPrompt)
	return &SessionStoreSummary{
		SessionID:   key.SessionID,
		Summary:     summary,
		CustomTitle: customTitle,
		FirstPrompt: firstPrompt,
		GitBranch:   stringFromAny(data["git_branch"]),
		CWD:         stringFromAny(data["cwd"]),
		Tag:         stringFromAny(data["tag"]),
		CreatedAt:   int64(intFromAny(data["created_at"])),
		Data:        data,
	}
}

func foldSessionSummaryFirstPrompt(data map[string]any, entry SessionStoreEntry) {
	if boolFromAny(data["first_prompt_locked"]) || stringFromAny(entry["type"]) != "user" || boolFromAny(entry["isMeta"]) || boolFromAny(entry["isCompactSummary"]) {
		return
	}
	message := mapFromAny(entry["message"])
	if message == nil || sessionStoreContentHasToolResult(message["content"]) {
		return
	}
	for _, raw := range sessionStoreTextBlocks(message["content"]) {
		prompt := strings.TrimSpace(strings.ReplaceAll(raw, "\n", " "))
		if prompt == "" {
			continue
		}
		if command := commandNameFromPrompt(prompt); command != "" {
			if stringFromAny(data["command_fallback"]) == "" {
				data["command_fallback"] = command
			}
			continue
		}
		if skipFirstPromptPattern.MatchString(prompt) {
			continue
		}
		data["first_prompt"] = truncateSessionPrompt(prompt, 200)
		data["first_prompt_locked"] = true
		return
	}
}

func sessionStoreContentHasToolResult(content any) bool {
	blocks, ok := content.([]any)
	if !ok {
		return false
	}
	for _, raw := range blocks {
		if stringFromAny(mapFromAny(raw)["type"]) == "tool_result" {
			return true
		}
	}
	return false
}

func sessionStoreTextBlocks(content any) []string {
	switch value := content.(type) {
	case string:
		return []string{value}
	case []any:
		texts := make([]string, 0, len(value))
		for _, raw := range value {
			block := mapFromAny(raw)
			if stringFromAny(block["type"]) == "text" {
				if text, ok := block["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
		return texts
	default:
		return nil
	}
}

func commandNameFromPrompt(prompt string) string {
	const start = "<command-name>"
	const end = "</command-name>"
	startAt := strings.Index(prompt, start)
	if startAt < 0 {
		return ""
	}
	endAt := strings.Index(prompt[startAt+len(start):], end)
	if endAt < 0 {
		return ""
	}
	return prompt[startAt+len(start) : startAt+len(start)+endAt]
}

func truncateSessionPrompt(prompt string, maxRunes int) string {
	if utf8.RuneCountInString(prompt) <= maxRunes {
		return prompt
	}
	runes := []rune(prompt)
	return strings.TrimRightFunc(string(runes[:maxRunes]), unicode.IsSpace) + "…"
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
