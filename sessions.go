package claudeagentsdk

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const liteReadBufSize = 64 * 1024

var (
	uuidPattern            = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
	sanitizePathPattern    = regexp.MustCompile(`[^a-zA-Z0-9]`)
	skipFirstPromptPattern = regexp.MustCompile(`^(?:<local-command-stdout>|<session-start-hook>|<tick>|<goal>|\[Request interrupted by user[^\]]*\]|\s*<ide_opened_file>[\s\S]*</ide_opened_file>\s*$|\s*<ide_selection>[\s\S]*</ide_selection>\s*$)`)
	transcriptMessageTypes = []string{"user", "assistant", "attachment", "system", "progress"}
)

type SessionInfo struct {
	SessionID    string
	Summary      string
	LastModified int64
	FileSize     int64
	CustomTitle  string
	FirstPrompt  string
	GitBranch    string
	CWD          string
	Tag          string
	CreatedAt    int64
}

type SessionRecord struct {
	Type            string
	UUID            string
	SessionID       string
	Message         any
	ParentToolUseID any
}

type ForkResult struct {
	SessionID string
}

func ProjectKeyForDirectory(directory string) (string, error) {
	if directory == "" {
		directory = "."
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		abs, absErr := filepath.Abs(directory)
		if absErr != nil {
			return "", absErr
		}
		canonical = abs
	}
	return sanitizePath(canonical), nil
}

func ListSessions(directory string) ([]SessionInfo, error) {
	projectDir, err := findProjectDirForDirectory(directory)
	if err != nil {
		return nil, err
	}
	if projectDir == "" {
		return []SessionInfo{}, nil
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SessionInfo{}, nil
		}
		return nil, err
	}

	results := make([]SessionInfo, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		sessionID := strings.TrimSuffix(entry.Name(), ".jsonl")
		if !isUUID(sessionID) {
			continue
		}
		info, err := parseSessionInfo(filepath.Join(projectDir, entry.Name()), sessionID, directory)
		if err != nil || info == nil {
			continue
		}
		results = append(results, *info)
	}

	slices.SortFunc(results, func(a, b SessionInfo) int {
		switch {
		case a.LastModified > b.LastModified:
			return -1
		case a.LastModified < b.LastModified:
			return 1
		default:
			return strings.Compare(a.SessionID, b.SessionID)
		}
	})
	return results, nil
}

func GetSessionInfo(sessionID string, directory string) (*SessionInfo, error) {
	if !isUUID(sessionID) {
		return nil, fmt.Errorf("invalid session_id: %s", sessionID)
	}
	path, err := findSessionFile(sessionID, directory)
	if err != nil || path == "" {
		return nil, err
	}
	return parseSessionInfo(path, sessionID, directory)
}

func GetSessionMessages(sessionID string, directory string, limit int, offset int) ([]SessionRecord, error) {
	if !isUUID(sessionID) {
		return []SessionRecord{}, nil
	}
	path, err := findSessionFile(sessionID, directory)
	if err != nil || path == "" {
		return []SessionRecord{}, err
	}

	entries, err := readTranscriptEntries(path)
	if err != nil {
		return nil, err
	}
	return conversationRecords(entries, limit, offset), nil
}

// ListSubagents returns the IDs of subagent transcripts belonging to a session.
func ListSubagents(sessionID string, directory string) ([]string, error) {
	if !isUUID(sessionID) {
		return []string{}, nil
	}
	path, err := findSessionFile(sessionID, directory)
	if err != nil || path == "" {
		return []string{}, err
	}
	files, err := collectSubagentFiles(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(files))
	for _, file := range files {
		ids = append(ids, file.id)
	}
	return ids, nil
}

// GetSubagentMessages returns the visible user and assistant chain for a subagent.
func GetSubagentMessages(sessionID string, agentID string, directory string, limit int, offset int) ([]SessionRecord, error) {
	if !isUUID(sessionID) || agentID == "" {
		return []SessionRecord{}, nil
	}
	path, err := findSessionFile(sessionID, directory)
	if err != nil || path == "" {
		return []SessionRecord{}, err
	}
	files, err := collectSubagentFiles(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents"))
	if errors.Is(err, os.ErrNotExist) {
		return []SessionRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.id != agentID {
			continue
		}
		entries, err := readTranscriptEntries(file.path)
		if err != nil {
			return nil, err
		}
		return subagentRecords(entries, limit, offset), nil
	}
	return []SessionRecord{}, nil
}

// ListSubagentsFromStore returns the IDs of subagent transcripts in a SessionStore.
func ListSubagentsFromStore(store SessionStore, sessionID string, directory string) ([]string, error) {
	if !isUUID(sessionID) {
		return []string{}, nil
	}
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		return nil, err
	}
	subkeyStore, ok := store.(SessionSubkeyStore)
	if !ok {
		return nil, fmt.Errorf("session store does not implement ListSubkeys; listing subagents requires SessionSubkeyStore")
	}
	subkeys, err := subkeyStore.ListSubkeys(SessionListSubkeysKey{ProjectKey: projectKey, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	ids := make([]string, 0)
	for _, subkey := range subkeys {
		id := subagentIDFromSubpath(subkey)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetSubagentMessagesFromStore returns the visible user and assistant chain for a stored subagent transcript.
func GetSubagentMessagesFromStore(store SessionStore, sessionID string, agentID string, directory string, limit int, offset int) ([]SessionRecord, error) {
	if !isUUID(sessionID) || agentID == "" {
		return []SessionRecord{}, nil
	}
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		return nil, err
	}
	subpath := "subagents/agent-" + agentID
	if subkeyStore, ok := store.(SessionSubkeyStore); ok {
		subkeys, err := subkeyStore.ListSubkeys(SessionListSubkeysKey{ProjectKey: projectKey, SessionID: sessionID})
		if err != nil {
			return nil, err
		}
		subpath = ""
		for _, candidate := range subkeys {
			if subagentIDFromSubpath(candidate) == agentID {
				subpath = candidate
				break
			}
		}
		if subpath == "" {
			return []SessionRecord{}, nil
		}
	}
	entries, err := store.Load(SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath})
	if err != nil {
		return nil, err
	}
	raw := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if stringFromAny(entry["type"]) != "agent_metadata" {
			raw = append(raw, entry)
		}
	}
	return subagentRecords(raw, limit, offset), nil
}

// ListSessionsFromStore lists sessions maintained by a SessionStore.
func ListSessionsFromStore(store SessionStore, directory string, limit int, offset int) ([]SessionInfo, error) {
	listStore, ok := store.(SessionListStore)
	if !ok {
		return nil, fmt.Errorf("session store does not implement ListSessions; listing sessions requires SessionListStore")
	}
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		return nil, err
	}
	list, err := listStore.ListSessions(projectKey)
	if err != nil {
		return nil, err
	}
	infos := make([]SessionInfo, 0, len(list))
	for _, item := range list {
		info, err := GetSessionInfoFromStore(store, item.SessionID, directory)
		if err != nil || info == nil {
			continue
		}
		info.LastModified = item.MTime
		infos = append(infos, *info)
	}
	slices.SortFunc(infos, func(a, b SessionInfo) int {
		switch {
		case a.LastModified > b.LastModified:
			return -1
		case a.LastModified < b.LastModified:
			return 1
		default:
			return strings.Compare(a.SessionID, b.SessionID)
		}
	})
	if offset > 0 {
		if offset >= len(infos) {
			return []SessionInfo{}, nil
		}
		infos = infos[offset:]
	}
	if limit > 0 && limit < len(infos) {
		infos = infos[:limit]
	}
	return infos, nil
}

// GetSessionInfoFromStore returns metadata derived from a stored transcript.
func GetSessionInfoFromStore(store SessionStore, sessionID string, directory string) (*SessionInfo, error) {
	if !isUUID(sessionID) {
		return nil, nil
	}
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		return nil, err
	}
	entries, err := store.Load(SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return sessionInfoFromStoreEntries(entries, sessionID, directory), nil
}

// GetSessionMessagesFromStore returns the visible conversation chain from a stored transcript.
func GetSessionMessagesFromStore(store SessionStore, sessionID string, directory string, limit int, offset int) ([]SessionRecord, error) {
	if !isUUID(sessionID) {
		return []SessionRecord{}, nil
	}
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		return nil, err
	}
	entries, err := store.Load(SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	if err != nil || len(entries) == 0 {
		return []SessionRecord{}, err
	}
	transcript := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if isTranscriptEntry(entry) {
			transcript = append(transcript, entry)
		}
	}
	return conversationRecords(transcript, limit, offset), nil
}

// RenameSessionViaStore appends a custom title entry to a SessionStore.
func RenameSessionViaStore(store SessionStore, sessionID string, title string, directory string) error {
	if !isUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("title must be non-empty")
	}
	return appendStoreSessionEntry(store, sessionID, directory, SessionStoreEntry{
		"type": "custom-title", "customTitle": title, "sessionId": sessionID, "uuid": newUUID(), "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// TagSessionViaStore appends a tag entry to a SessionStore.
func TagSessionViaStore(store SessionStore, sessionID string, tag string, directory string) error {
	if !isUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	tag = strings.TrimSpace(sanitizeUnicode(tag))
	if tag == "" {
		return fmt.Errorf("tag must be non-empty")
	}
	return appendStoreSessionEntry(store, sessionID, directory, SessionStoreEntry{
		"type": "tag", "tag": tag, "sessionId": sessionID, "uuid": newUUID(), "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// ClearSessionTagViaStore clears a session tag in a SessionStore.
func ClearSessionTagViaStore(store SessionStore, sessionID string, directory string) error {
	if !isUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	return appendStoreSessionEntry(store, sessionID, directory, SessionStoreEntry{
		"type": "tag", "tag": "", "sessionId": sessionID, "uuid": newUUID(), "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// DeleteSessionViaStore deletes a transcript when the store supports SessionDeleteStore.
func DeleteSessionViaStore(store SessionStore, sessionID string, directory string) error {
	if !isUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	deleteStore, ok := store.(SessionDeleteStore)
	if !ok {
		return nil
	}
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		return err
	}
	return deleteStore.Delete(SessionKey{ProjectKey: projectKey, SessionID: sessionID})
}

// ForkSessionViaStore forks a stored transcript using the same transform as local sessions.
func ForkSessionViaStore(store SessionStore, sessionID string, directory string, upToMessageID string, title string) (*ForkResult, error) {
	if !isUUID(sessionID) {
		return nil, fmt.Errorf("invalid session_id: %s", sessionID)
	}
	if upToMessageID != "" && !isUUID(upToMessageID) {
		return nil, fmt.Errorf("invalid up_to_message_id: %s", upToMessageID)
	}
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		return nil, err
	}
	entries, err := store.Load(SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	content, err := sessionStoreEntriesJSONL(entries)
	if err != nil {
		return nil, err
	}
	transcript, replacements := parseForkTranscript(content, sessionID)
	info := sessionInfoFromStoreEntries(entries, sessionID, directory)
	derivedTitle := ""
	if info != nil {
		derivedTitle = firstNonEmpty(info.CustomTitle, info.Summary)
	}
	forkedID, lines, err := buildForkLines(transcript, replacements, sessionID, upToMessageID, title, derivedTitle)
	if err != nil {
		return nil, err
	}
	forkEntries := make([]SessionStoreEntry, 0, len(lines))
	for _, line := range lines {
		var entry SessionStoreEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, err
		}
		forkEntries = append(forkEntries, entry)
	}
	if err := store.Append(SessionKey{ProjectKey: projectKey, SessionID: forkedID}, forkEntries); err != nil {
		return nil, err
	}
	return &ForkResult{SessionID: forkedID}, nil
}

// ImportSessionToStore replays a local session transcript into a SessionStore.
// Subagent transcripts and their metadata sidecars are included when requested.
func ImportSessionToStore(store SessionStore, sessionID string, directory string, includeSubagents bool, batchSize int) error {
	if !isUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	path, err := findSessionFile(sessionID, directory)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("session %s not found", sessionID)
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	projectKey := filepath.Base(filepath.Dir(path))
	mainKey := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	if err := appendJSONLFileToStore(store, mainKey, path, batchSize); err != nil {
		return err
	}
	if !includeSubagents {
		return nil
	}

	sessionDir := strings.TrimSuffix(path, ".jsonl")
	for _, subagentPath := range collectJSONLFiles(filepath.Join(sessionDir, "subagents")) {
		rel, err := filepath.Rel(sessionDir, subagentPath)
		if err != nil {
			return err
		}
		subpath := strings.TrimSuffix(filepath.ToSlash(rel), ".jsonl")
		key := SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath}
		if err := appendJSONLFileToStore(store, key, subagentPath, batchSize); err != nil {
			return err
		}
		metaPath := strings.TrimSuffix(subagentPath, ".jsonl") + ".meta.json"
		meta, err := os.ReadFile(metaPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		metadata := SessionStoreEntry{"type": "agent_metadata"}
		if err := json.Unmarshal(meta, &metadata); err != nil {
			return fmt.Errorf("parsing subagent metadata %s: %w", metaPath, err)
		}
		metadata["type"] = "agent_metadata"
		if err := store.Append(key, []SessionStoreEntry{metadata}); err != nil {
			return err
		}
	}
	return nil
}

func appendJSONLFileToStore(store SessionStore, key SessionKey, path string, batchSize int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	batch := make([]SessionStoreEntry, 0, batchSize)
	batchBytes := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry SessionStoreEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("parsing session transcript %s: %w", path, err)
		}
		batch = append(batch, entry)
		batchBytes += len(line)
		if len(batch) >= batchSize || batchBytes >= 1<<20 {
			if err := store.Append(key, batch); err != nil {
				return err
			}
			batch = make([]SessionStoreEntry, 0, batchSize)
			batchBytes = 0
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(batch) > 0 {
		return store.Append(key, batch)
	}
	return nil
}

func collectJSONLFiles(baseDir string) []string {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}
	files := make([]string, 0)
	for _, entry := range entries {
		path := filepath.Join(baseDir, entry.Name())
		if entry.IsDir() {
			files = append(files, collectJSONLFiles(path)...)
		} else if strings.HasSuffix(entry.Name(), ".jsonl") {
			files = append(files, path)
		}
	}
	return files
}

func appendStoreSessionEntry(store SessionStore, sessionID string, directory string, entry SessionStoreEntry) error {
	projectKey, err := ProjectKeyForDirectory(directory)
	if err != nil {
		return err
	}
	return store.Append(SessionKey{ProjectKey: projectKey, SessionID: sessionID}, []SessionStoreEntry{entry})
}

func sessionInfoFromStoreEntries(entries []SessionStoreEntry, sessionID string, directory string) *SessionInfo {
	var customTitle, lastPrompt, summary, gitBranch, cwd, tag, firstPrompt string
	var createdAt int64
	var fileSize int64
	for _, entry := range entries {
		data, _ := json.Marshal(entry)
		fileSize += int64(len(data) + 1)
		if custom := stringFromAny(entry["customTitle"]); custom != "" {
			customTitle = custom
		}
		if title := stringFromAny(entry["aiTitle"]); title != "" && customTitle == "" {
			customTitle = title
		}
		if value := stringFromAny(entry["lastPrompt"]); value != "" {
			lastPrompt = value
		}
		if value := stringFromAny(entry["summary"]); value != "" {
			summary = value
		}
		if value := stringFromAny(entry["gitBranch"]); value != "" {
			gitBranch = value
		}
		if value := stringFromAny(entry["cwd"]); value != "" && cwd == "" {
			cwd = value
		}
		if stringFromAny(entry["type"]) == "tag" {
			tag = stringFromAny(entry["tag"])
		}
		if createdAt == 0 {
			createdAt = parseCreatedAt(stringFromAny(entry["timestamp"]))
		}
		if firstPrompt == "" && stringFromAny(entry["type"]) == "user" {
			message := mapFromAny(entry["message"])
			prompt := strings.TrimSpace(extractMessageText(message["content"]))
			if prompt != "" && !skipFirstPromptPattern.MatchString(prompt) {
				firstPrompt = prompt
			}
		}
	}
	resolvedSummary := firstNonEmpty(customTitle, lastPrompt, summary, firstPrompt)
	if resolvedSummary == "" {
		return nil
	}
	if cwd == "" {
		cwd = directory
	}
	return &SessionInfo{SessionID: sessionID, Summary: resolvedSummary, FileSize: fileSize, CustomTitle: customTitle, FirstPrompt: firstPrompt, GitBranch: gitBranch, CWD: cwd, Tag: tag, CreatedAt: createdAt}
}

func sessionStoreEntriesJSONL(entries []SessionStoreEntry) ([]byte, error) {
	var content bytes.Buffer
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		content.Write(line)
		content.WriteByte('\n')
	}
	return content.Bytes(), nil
}

type subagentFile struct {
	id   string
	path string
}

func collectSubagentFiles(baseDir string) ([]subagentFile, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, err
	}
	files := make([]subagentFile, 0)
	for _, entry := range entries {
		path := filepath.Join(baseDir, entry.Name())
		if entry.IsDir() {
			nested, err := collectSubagentFiles(path)
			if err != nil {
				return nil, err
			}
			files = append(files, nested...)
			continue
		}
		if !strings.HasPrefix(entry.Name(), "agent-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		files = append(files, subagentFile{
			id:   strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "agent-"), ".jsonl"),
			path: path,
		})
	}
	return files, nil
}

func subagentIDFromSubpath(subpath string) string {
	if !strings.HasPrefix(subpath, "subagents/") {
		return ""
	}
	name := filepath.Base(subpath)
	if !strings.HasPrefix(name, "agent-") {
		return ""
	}
	return strings.TrimPrefix(name, "agent-")
}

func readTranscriptEntries(path string) ([]map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	entries := make([]map[string]any, 0)
	for scanner.Scan() {
		var entry map[string]any
		if json.Unmarshal(scanner.Bytes(), &entry) == nil && isTranscriptEntry(entry) {
			entries = append(entries, entry)
		}
	}
	return entries, scanner.Err()
}

func isTranscriptEntry(entry map[string]any) bool {
	if stringFromAny(entry["uuid"]) == "" {
		return false
	}
	switch stringFromAny(entry["type"]) {
	case "user", "assistant", "progress", "system", "attachment":
		return true
	default:
		return false
	}
}

func conversationRecords(entries []map[string]any, limit int, offset int) []SessionRecord {
	chain := buildConversationChain(entries)
	records := make([]SessionRecord, 0, len(chain))
	for _, entry := range chain {
		if !isVisibleConversationEntry(entry) {
			continue
		}
		records = append(records, sessionRecordFromEntry(entry))
	}
	return paginateSessionRecords(records, limit, offset)
}

func buildConversationChain(entries []map[string]any) []map[string]any {
	byUUID := make(map[string]map[string]any, len(entries))
	index := make(map[string]int, len(entries))
	parents := make(map[string]struct{}, len(entries))
	for i, entry := range entries {
		uuid := stringFromAny(entry["uuid"])
		byUUID[uuid] = entry
		index[uuid] = i
		if parent := stringFromAny(entry["parentUuid"]); parent != "" {
			parents[parent] = struct{}{}
		}
	}

	var leaf map[string]any
	var fallbackLeaf map[string]any
	leafIndex := -1
	fallbackIndex := -1
	for _, terminal := range entries {
		if _, hasChild := parents[stringFromAny(terminal["uuid"])]; hasChild {
			continue
		}
		candidate := terminal
		seen := map[string]struct{}{}
		for candidate != nil {
			uuid := stringFromAny(candidate["uuid"])
			if _, duplicate := seen[uuid]; duplicate {
				break
			}
			seen[uuid] = struct{}{}
			typ := stringFromAny(candidate["type"])
			if typ == "user" || typ == "assistant" {
				candidateIndex := index[uuid]
				if candidateIndex > fallbackIndex {
					fallbackLeaf = candidate
					fallbackIndex = candidateIndex
				}
				if !boolFromAny(candidate["isSidechain"]) && stringFromAny(candidate["teamName"]) == "" && !boolFromAny(candidate["isMeta"]) && candidateIndex > leafIndex {
					leaf = candidate
					leafIndex = candidateIndex
				}
				break
			}
			candidate = byUUID[stringFromAny(candidate["parentUuid"])]
		}
	}
	if leaf == nil {
		leaf = fallbackLeaf
	}
	if leaf == nil {
		return []map[string]any{}
	}

	chain := make([]map[string]any, 0)
	seen := map[string]struct{}{}
	for current := leaf; current != nil; current = byUUID[stringFromAny(current["parentUuid"])] {
		uuid := stringFromAny(current["uuid"])
		if _, duplicate := seen[uuid]; duplicate {
			break
		}
		seen[uuid] = struct{}{}
		chain = append(chain, current)
	}
	slices.Reverse(chain)
	return chain
}

func isVisibleConversationEntry(entry map[string]any) bool {
	typ := stringFromAny(entry["type"])
	return (typ == "user" || typ == "assistant") && !boolFromAny(entry["isMeta"]) && !boolFromAny(entry["isSidechain"]) && stringFromAny(entry["teamName"]) == ""
}

func sessionRecordFromEntry(entry map[string]any) SessionRecord {
	return SessionRecord{
		Type:            stringFromAny(entry["type"]),
		UUID:            stringFromAny(entry["uuid"]),
		SessionID:       stringFromAny(entry["sessionId"]),
		Message:         entry["message"],
		ParentToolUseID: nil,
	}
}

func paginateSessionRecords(records []SessionRecord, limit int, offset int) []SessionRecord {
	if offset > 0 {
		if offset >= len(records) {
			return []SessionRecord{}
		}
		records = records[offset:]
	}
	if limit > 0 && limit < len(records) {
		records = records[:limit]
	}
	return records
}

func subagentRecords(entries []map[string]any, limit int, offset int) []SessionRecord {
	byUUID := make(map[string]map[string]any, len(entries))
	var leaf map[string]any
	for _, entry := range entries {
		if uuid := stringFromAny(entry["uuid"]); uuid != "" {
			byUUID[uuid] = entry
		}
		typ := stringFromAny(entry["type"])
		if typ == "user" || typ == "assistant" {
			leaf = entry
		}
	}
	chain := make([]map[string]any, 0)
	seen := map[string]struct{}{}
	for current := leaf; current != nil; {
		uuid := stringFromAny(current["uuid"])
		if uuid == "" {
			break
		}
		if _, exists := seen[uuid]; exists {
			break
		}
		seen[uuid] = struct{}{}
		chain = append(chain, current)
		current = byUUID[stringFromAny(current["parentUuid"])]
	}
	slices.Reverse(chain)
	records := make([]SessionRecord, 0, len(chain))
	for _, entry := range chain {
		records = append(records, SessionRecord{
			Type:            stringFromAny(entry["type"]),
			UUID:            stringFromAny(entry["uuid"]),
			SessionID:       stringFromAny(entry["sessionId"]),
			Message:         entry["message"],
			ParentToolUseID: nil,
		})
	}
	if offset > 0 {
		if offset >= len(records) {
			return []SessionRecord{}
		}
		records = records[offset:]
	}
	if limit > 0 && limit < len(records) {
		records = records[:limit]
	}
	return records
}

func RenameSession(sessionID string, title string, directory string) error {
	if !isUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	stripped := strings.TrimSpace(title)
	if stripped == "" {
		return fmt.Errorf("title must be non-empty")
	}
	entry := map[string]any{
		"type":        "custom-title",
		"customTitle": stripped,
		"sessionId":   sessionID,
	}
	return appendSessionEntry(sessionID, directory, entry)
}

func TagSession(sessionID string, tag string, directory string) error {
	if !isUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	sanitized := sanitizeUnicode(tag)
	if strings.TrimSpace(sanitized) == "" {
		return fmt.Errorf("tag must be non-empty")
	}
	entry := map[string]any{
		"type":      "tag",
		"tag":       strings.TrimSpace(sanitized),
		"sessionId": sessionID,
	}
	return appendSessionEntry(sessionID, directory, entry)
}

func ClearSessionTag(sessionID string, directory string) error {
	if !isUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	entry := map[string]any{
		"type":      "tag",
		"tag":       "",
		"sessionId": sessionID,
	}
	return appendSessionEntry(sessionID, directory, entry)
}

func DeleteSession(sessionID string, directory string) error {
	if !isUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	path, err := findSessionFile(sessionID, directory)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("session %s not found", sessionID)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(filepath.Dir(path), sessionID))
	return nil
}

func ForkSession(sessionID string, directory string, upToMessageID string, title string) (*ForkResult, error) {
	if !isUUID(sessionID) {
		return nil, fmt.Errorf("invalid session_id: %s", sessionID)
	}
	if upToMessageID != "" && !isUUID(upToMessageID) {
		return nil, fmt.Errorf("invalid up_to_message_id: %s", upToMessageID)
	}
	path, err := findSessionFile(sessionID, directory)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	transcript, replacements := parseForkTranscript(content, sessionID)
	if len(transcript) == 0 {
		return nil, fmt.Errorf("session %s has no messages to fork", sessionID)
	}
	info, _ := parseSessionInfo(path, sessionID, directory)
	derivedTitle := ""
	if info != nil {
		if info.CustomTitle != "" {
			derivedTitle = info.CustomTitle
		} else if info.Summary != "" {
			derivedTitle = info.Summary
		}
	}
	forkedID, lines, err := buildForkLines(transcript, replacements, sessionID, upToMessageID, title, derivedTitle)
	if err != nil {
		return nil, err
	}
	forkPath := filepath.Join(filepath.Dir(path), forkedID+".jsonl")
	if err := os.WriteFile(forkPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return nil, err
	}
	return &ForkResult{SessionID: forkedID}, nil
}

func appendSessionEntry(sessionID string, directory string, entry map[string]any) error {
	path, err := findSessionFile(sessionID, directory)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("session %s not found", sessionID)
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(payload)
	return err
}

func parseSessionInfo(path string, sessionID string, projectPath string) (*SessionInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Size() == 0 {
		return nil, nil
	}

	headBuf := make([]byte, minInt64(stat.Size(), liteReadBufSize))
	_, err = io.ReadFull(file, headBuf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	head := string(headBuf)
	tail := head
	if stat.Size() > liteReadBufSize {
		if _, err := file.Seek(-liteReadBufSize, io.SeekEnd); err == nil {
			tailBuf := make([]byte, liteReadBufSize)
			n, _ := io.ReadFull(file, tailBuf)
			tail = string(tailBuf[:n])
		}
	}

	firstLine := head
	if idx := strings.Index(head, "\n"); idx >= 0 {
		firstLine = head[:idx]
	}
	if strings.Contains(firstLine, `"isSidechain":true`) || strings.Contains(firstLine, `"isSidechain": true`) {
		return nil, nil
	}

	customTitle := firstNonEmpty(
		extractLastJSONField(tail, "customTitle"),
		extractLastJSONField(head, "customTitle"),
		extractLastJSONField(tail, "aiTitle"),
		extractLastJSONField(head, "aiTitle"),
	)
	firstPrompt := extractFirstPromptFromHead(head)
	summary := firstNonEmpty(
		customTitle,
		extractLastJSONField(tail, "lastPrompt"),
		extractLastJSONField(tail, "summary"),
		firstPrompt,
	)
	if summary == "" {
		return nil, nil
	}

	gitBranch := firstNonEmpty(
		extractLastJSONField(tail, "gitBranch"),
		extractJSONField(head, "gitBranch"),
	)
	cwd := firstNonEmpty(extractJSONField(head, "cwd"), projectPath)
	tag := extractLastTag(tail)
	createdAt := parseCreatedAt(extractJSONField(head, "timestamp"))

	return &SessionInfo{
		SessionID:    sessionID,
		Summary:      summary,
		LastModified: stat.ModTime().UnixMilli(),
		FileSize:     stat.Size(),
		CustomTitle:  customTitle,
		FirstPrompt:  firstPrompt,
		GitBranch:    gitBranch,
		CWD:          cwd,
		Tag:          tag,
		CreatedAt:    createdAt,
	}, nil
}

func findProjectDirForDirectory(directory string) (string, error) {
	key, err := ProjectKeyForDirectory(directory)
	if err != nil {
		return "", err
	}
	return filepath.Join(getProjectsDir(), key), nil
}

func findSessionFile(sessionID string, directory string) (string, error) {
	if directory != "" {
		projectDir, err := findProjectDirForDirectory(directory)
		if err != nil {
			return "", err
		}
		path := filepath.Join(projectDir, sessionID+".jsonl")
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			return path, nil
		}
		return "", nil
	}

	entries, err := os.ReadDir(getProjectsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(getProjectsDir(), entry.Name(), sessionID+".jsonl")
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			return path, nil
		}
	}
	return "", nil
}

func getProjectsDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

func sanitizePath(name string) string {
	sanitized := sanitizePathPattern.ReplaceAllString(name, "-")
	if len(sanitized) <= 200 {
		return sanitized
	}
	return sanitized[:200] + "-" + simpleHash(name)
}

func simpleHash(s string) string {
	h := int32(0)
	for _, ch := range s {
		h = (h << 5) - h + int32(ch)
	}
	n := h
	if n < 0 {
		n = -n
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{digits[n%36]}, out...)
		n /= 36
	}
	return string(out)
}

func extractJSONField(text string, key string) string {
	patterns := []string{fmt.Sprintf(`"%s":"`, key), fmt.Sprintf(`"%s": "`, key)}
	for _, pattern := range patterns {
		idx := strings.Index(text, pattern)
		if idx < 0 {
			continue
		}
		start := idx + len(pattern)
		for i := start; i < len(text); i++ {
			if text[i] == '\\' {
				i++
				continue
			}
			if text[i] == '"' {
				return unescapeJSONString(text[start:i])
			}
		}
	}
	return ""
}

func extractLastJSONField(text string, key string) string {
	last := ""
	patterns := []string{fmt.Sprintf(`"%s":"`, key), fmt.Sprintf(`"%s": "`, key)}
	for _, pattern := range patterns {
		searchFrom := 0
		for {
			idx := strings.Index(text[searchFrom:], pattern)
			if idx < 0 {
				break
			}
			idx += searchFrom
			start := idx + len(pattern)
			for i := start; i < len(text); i++ {
				if text[i] == '\\' {
					i++
					continue
				}
				if text[i] == '"' {
					last = unescapeJSONString(text[start:i])
					searchFrom = i + 1
					break
				}
				if i == len(text)-1 {
					searchFrom = len(text)
				}
			}
			if searchFrom >= len(text) {
				break
			}
		}
	}
	return last
}

func extractFirstPromptFromHead(head string) string {
	lines := strings.Split(head, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if stringFromAny(entry["type"]) != "user" {
			continue
		}
		message, _ := entry["message"].(map[string]any)
		content := strings.TrimSpace(extractMessageText(message["content"]))
		if content == "" || skipFirstPromptPattern.MatchString(content) {
			continue
		}
		return content
	}
	return ""
}

func extractMessageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, _ := item.(map[string]any)
			if stringFromAny(block["type"]) == "text" {
				parts = append(parts, stringFromAny(block["text"]))
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func extractLastTag(tail string) string {
	lines := strings.Split(tail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if stringFromAny(entry["type"]) != "tag" {
			continue
		}
		return stringFromAny(entry["tag"])
	}
	return ""
}

func parseCreatedAt(value string) int64 {
	if value == "" {
		return 0
	}
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0
	}
	return ts.UnixMilli()
}

func unescapeJSONString(raw string) string {
	if !strings.Contains(raw, `\`) {
		return raw
	}
	var decoded string
	if err := json.Unmarshal([]byte(`"`+raw+`"`), &decoded); err != nil {
		return raw
	}
	return decoded
}

func parseForkTranscript(content []byte, sessionID string) ([]map[string]any, []any) {
	transcript := make([]map[string]any, 0)
	replacements := make([]any, 0)
	scanner := bufio.NewScanner(bytes.NewReader(content))
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 8*1024*1024)

	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		entryType := stringFromAny(entry["type"])
		if slices.Contains(transcriptMessageTypes, entryType) && stringFromAny(entry["uuid"]) != "" {
			transcript = append(transcript, entry)
			continue
		}
		if entryType == "content-replacement" && stringFromAny(entry["sessionId"]) == sessionID {
			if items, ok := entry["replacements"].([]any); ok {
				replacements = append(replacements, items...)
			}
		}
	}
	return transcript, replacements
}

func buildForkLines(transcript []map[string]any, replacements []any, sessionID string, upToMessageID string, title string, derivedTitle string) (string, []string, error) {
	filtered := make([]map[string]any, 0, len(transcript))
	for _, entry := range transcript {
		if boolFromAny(entry["isSidechain"]) {
			continue
		}
		filtered = append(filtered, entry)
	}
	if len(filtered) == 0 {
		return "", nil, fmt.Errorf("session %s has no messages to fork", sessionID)
	}
	if upToMessageID != "" {
		cutoff := -1
		for i, entry := range filtered {
			if stringFromAny(entry["uuid"]) == upToMessageID {
				cutoff = i
				break
			}
		}
		if cutoff < 0 {
			return "", nil, fmt.Errorf("message %s not found in session %s", upToMessageID, sessionID)
		}
		filtered = filtered[:cutoff+1]
	}

	uuidMap := map[string]string{}
	for _, entry := range filtered {
		uuidMap[stringFromAny(entry["uuid"])] = newUUID()
	}
	byUUID := map[string]map[string]any{}
	writable := make([]map[string]any, 0, len(filtered))
	for _, entry := range filtered {
		byUUID[stringFromAny(entry["uuid"])] = entry
		if stringFromAny(entry["type"]) != "progress" {
			writable = append(writable, entry)
		}
	}
	if len(writable) == 0 {
		return "", nil, fmt.Errorf("session %s has no messages to fork", sessionID)
	}

	forkedSessionID := newUUID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	lines := make([]string, 0, len(writable)+2)
	for i, original := range writable {
		cloned := map[string]any{}
		for k, v := range original {
			cloned[k] = v
		}
		parentID := stringFromAny(original["parentUuid"])
		newParent := ""
		for parentID != "" {
			parent := byUUID[parentID]
			if parent == nil {
				break
			}
			if stringFromAny(parent["type"]) != "progress" {
				newParent = uuidMap[parentID]
				break
			}
			parentID = stringFromAny(parent["parentUuid"])
		}
		cloned["uuid"] = uuidMap[stringFromAny(original["uuid"])]
		if newParent != "" {
			cloned["parentUuid"] = newParent
		} else {
			cloned["parentUuid"] = nil
		}
		if logicalParent := stringFromAny(original["logicalParentUuid"]); logicalParent != "" {
			if mapped := uuidMap[logicalParent]; mapped != "" {
				cloned["logicalParentUuid"] = mapped
			}
		}
		cloned["sessionId"] = forkedSessionID
		if i == len(writable)-1 {
			cloned["timestamp"] = now
		}
		cloned["isSidechain"] = false
		cloned["forkedFrom"] = map[string]any{
			"sessionId":   sessionID,
			"messageUuid": stringFromAny(original["uuid"]),
		}
		delete(cloned, "teamName")
		delete(cloned, "agentName")
		delete(cloned, "slug")
		delete(cloned, "sourceToolAssistantUUID")
		body, _ := json.Marshal(cloned)
		lines = append(lines, string(body))
	}

	if len(replacements) > 0 {
		body, _ := json.Marshal(map[string]any{
			"type":         "content-replacement",
			"sessionId":    forkedSessionID,
			"replacements": replacements,
			"uuid":         newUUID(),
			"timestamp":    now,
		})
		lines = append(lines, string(body))
	}

	forkTitle := strings.TrimSpace(title)
	if forkTitle == "" {
		if derivedTitle == "" {
			derivedTitle = "Forked session"
		}
		forkTitle = derivedTitle + " (fork)"
	}
	body, _ := json.Marshal(map[string]any{
		"type":        "custom-title",
		"sessionId":   forkedSessionID,
		"customTitle": forkTitle,
		"uuid":        newUUID(),
		"timestamp":   now,
	})
	lines = append(lines, string(body))
	return forkedSessionID, lines, nil
}

func sanitizeUnicode(value string) string {
	var out []rune
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		if !utf8.ValidRune(r) {
			continue
		}
		out = append(out, r)
	}
	return strings.TrimSpace(string(out))
}

func newUUID() string {
	var b [16]byte
	if _, err := io.ReadFull(randReader{}, b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]),
		uint16(b[4])<<8|uint16(b[5]),
		uint16(b[6])<<8|uint16(b[7]),
		uint16(b[8])<<8|uint16(b[9]),
		uint64(b[10])<<40|uint64(b[11])<<32|uint64(b[12])<<24|uint64(b[13])<<16|uint64(b[14])<<8|uint64(b[15]),
	)
}

type randReader struct{}

func (randReader) Read(p []byte) (int, error) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Read(p)
}

func isUUID(v string) bool {
	return uuidPattern.MatchString(v)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func minInt64(a int64, b int) int {
	if a < int64(b) {
		return int(a)
	}
	return b
}
