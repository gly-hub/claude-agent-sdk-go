// Package testing provides reusable conformance checks for SessionStore adapters.
package testing

import (
	"reflect"
	stdtesting "testing"

	sdk "github.com/gly-hub/claude-agent-sdk-go"
)

// SessionStoreFactory creates an isolated store for one conformance contract.
type SessionStoreFactory func() sdk.SessionStore

// SessionStoreConformanceOptions skips checks for supported optional capabilities.
// A capability is checked automatically only when the returned store implements it.
type SessionStoreConformanceOptions struct {
	SkipListSessions         bool
	SkipListSessionSummaries bool
	SkipDelete               bool
	SkipListSubkeys          bool
}

var (
	conformanceKey = sdk.SessionKey{ProjectKey: "proj", SessionID: "sess"}
	conformanceSub = sdk.SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "subagents/agent-1"}
)

// RunSessionStoreConformance verifies the behavioral contract shared by every
// SessionStore adapter. The factory is invoked for each contract so adapters
// backed by external services can start from a known isolated state.
func RunSessionStoreConformance(t stdtesting.TB, makeStore SessionStoreFactory, options ...SessionStoreConformanceOptions) {
	t.Helper()
	if makeStore == nil {
		t.Fatal("SessionStoreFactory must not be nil")
	}
	opts := SessionStoreConformanceOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	fresh := func() sdk.SessionStore {
		t.Helper()
		store := makeStore()
		if store == nil {
			t.Fatal("SessionStoreFactory returned nil")
		}
		return store
	}
	entry := func(fields map[string]any) sdk.SessionStoreEntry {
		value := sdk.SessionStoreEntry{"type": "conformance"}
		for key, field := range fields {
			value[key] = field
		}
		return value
	}
	assertLoad := func(store sdk.SessionStore, key sdk.SessionKey, want []sdk.SessionStoreEntry) {
		t.Helper()
		got, err := store.Load(key)
		if err != nil {
			t.Fatalf("Load(%#v): %v", key, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Load(%#v) = %#v, want %#v", key, got, want)
		}
	}
	appendEntries := func(store sdk.SessionStore, key sdk.SessionKey, entries []sdk.SessionStoreEntry) {
		t.Helper()
		if err := store.Append(key, entries); err != nil {
			t.Fatalf("Append(%#v): %v", key, err)
		}
	}

	// Required: append/load preserve data and call order.
	store := fresh()
	want := []sdk.SessionStoreEntry{entry(map[string]any{"uuid": "b", "n": 1}), entry(map[string]any{"uuid": "a", "n": 2})}
	appendEntries(store, conformanceKey, want)
	assertLoad(store, conformanceKey, want)

	store = fresh()
	assertLoad(store, sdk.SessionKey{ProjectKey: "proj", SessionID: "unknown"}, nil)
	appendEntries(store, conformanceKey, []sdk.SessionStoreEntry{entry(map[string]any{"n": 1})})
	assertLoad(store, sdk.SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "unknown"}, nil)

	store = fresh()
	ordered := []sdk.SessionStoreEntry{entry(map[string]any{"uuid": "z"}), entry(map[string]any{"uuid": "a"}), entry(map[string]any{"uuid": "m"}), entry(map[string]any{"uuid": "b"})}
	appendEntries(store, conformanceKey, ordered[:1])
	appendEntries(store, conformanceKey, ordered[1:3])
	appendEntries(store, conformanceKey, ordered[3:])
	assertLoad(store, conformanceKey, ordered)
	appendEntries(store, conformanceKey, nil)
	assertLoad(store, conformanceKey, ordered)

	store = fresh()
	mainEntry := entry(map[string]any{"scope": "main"})
	subEntry := entry(map[string]any{"scope": "sub"})
	appendEntries(store, conformanceKey, []sdk.SessionStoreEntry{mainEntry})
	appendEntries(store, conformanceSub, []sdk.SessionStoreEntry{subEntry})
	assertLoad(store, conformanceKey, []sdk.SessionStoreEntry{mainEntry})
	assertLoad(store, conformanceSub, []sdk.SessionStoreEntry{subEntry})

	store = fresh()
	keyA := sdk.SessionKey{ProjectKey: "A", SessionID: "same"}
	keyB := sdk.SessionKey{ProjectKey: "B", SessionID: "same"}
	entryA := entry(map[string]any{"project": "A"})
	entryB := entry(map[string]any{"project": "B"})
	appendEntries(store, keyA, []sdk.SessionStoreEntry{entryA})
	appendEntries(store, keyB, []sdk.SessionStoreEntry{entryB})
	assertLoad(store, keyA, []sdk.SessionStoreEntry{entryA})
	assertLoad(store, keyB, []sdk.SessionStoreEntry{entryB})

	probe := fresh()
	listStore, supportsList := probe.(sdk.SessionListStore)
	_ = listStore
	if supportsList && !opts.SkipListSessions {
		store = fresh()
		listStore = store.(sdk.SessionListStore)
		appendEntries(store, sdk.SessionKey{ProjectKey: "proj", SessionID: "a"}, []sdk.SessionStoreEntry{entry(nil)})
		appendEntries(store, sdk.SessionKey{ProjectKey: "proj", SessionID: "b"}, []sdk.SessionStoreEntry{entry(nil)})
		appendEntries(store, sdk.SessionKey{ProjectKey: "other", SessionID: "c"}, []sdk.SessionStoreEntry{entry(nil)})
		listed, err := listStore.ListSessions("proj")
		if err != nil {
			t.Fatalf("ListSessions(): %v", err)
		}
		if !hasSessionIDs(listed, "a", "b") {
			t.Fatalf("ListSessions() = %#v, want session IDs a and b", listed)
		}
		for _, item := range listed {
			if item.MTime <= 1_000_000_000_000 {
				t.Fatalf("ListSessions() returned non-millisecond mtime: %#v", item)
			}
		}
		appendEntries(store, sdk.SessionKey{ProjectKey: "proj", SessionID: "a", Subpath: "subagents/agent-1"}, []sdk.SessionStoreEntry{entry(nil)})
		listed, err = listStore.ListSessions("proj")
		if err != nil || len(listed) != 2 {
			t.Fatalf("ListSessions() included a subpath or failed: %#v, %v", listed, err)
		}
	}

	if summaryStore, supportsSummaries := probe.(sdk.SessionSummaryStore); supportsSummaries && !opts.SkipListSessionSummaries {
		store = fresh()
		summaryStore = store.(sdk.SessionSummaryStore)
		appendEntries(store, sdk.SessionKey{ProjectKey: "proj", SessionID: "summary"}, []sdk.SessionStoreEntry{entry(nil)})
		summaries, err := summaryStore.ListSessionSummaries("proj")
		if err != nil || len(summaries) != 1 || summaries[0].SessionID != "summary" || summaries[0].LastModified <= 1_000_000_000_000 {
			t.Fatalf("ListSessionSummaries() = %#v, %v", summaries, err)
		}
	}

	if deleteStore, supportsDelete := probe.(sdk.SessionDeleteStore); supportsDelete && !opts.SkipDelete {
		store = fresh()
		deleteStore = store.(sdk.SessionDeleteStore)
		appendEntries(store, conformanceKey, []sdk.SessionStoreEntry{entry(nil)})
		appendEntries(store, conformanceSub, []sdk.SessionStoreEntry{entry(nil)})
		other := sdk.SessionKey{ProjectKey: "proj", SessionID: "other"}
		appendEntries(store, other, []sdk.SessionStoreEntry{entry(nil)})
		if err := deleteStore.Delete(conformanceKey); err != nil {
			t.Fatalf("Delete(main): %v", err)
		}
		assertLoad(store, conformanceKey, nil)
		assertLoad(store, conformanceSub, nil)
		assertLoad(store, other, []sdk.SessionStoreEntry{entry(nil)})

		appendEntries(store, conformanceKey, []sdk.SessionStoreEntry{entry(nil)})
		appendEntries(store, conformanceSub, []sdk.SessionStoreEntry{entry(nil)})
		if err := deleteStore.Delete(conformanceSub); err != nil {
			t.Fatalf("Delete(subpath): %v", err)
		}
		assertLoad(store, conformanceKey, []sdk.SessionStoreEntry{entry(nil)})
		assertLoad(store, conformanceSub, nil)
	}

	if subkeyStore, supportsSubkeys := probe.(sdk.SessionSubkeyStore); supportsSubkeys && !opts.SkipListSubkeys {
		store = fresh()
		subkeyStore = store.(sdk.SessionSubkeyStore)
		appendEntries(store, conformanceKey, []sdk.SessionStoreEntry{entry(nil)})
		appendEntries(store, conformanceSub, []sdk.SessionStoreEntry{entry(nil)})
		second := sdk.SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "subagents/agent-2"}
		appendEntries(store, second, []sdk.SessionStoreEntry{entry(nil)})
		subkeys, err := subkeyStore.ListSubkeys(sdk.SessionListSubkeysKey{ProjectKey: "proj", SessionID: "sess"})
		if err != nil || !reflect.DeepEqual(subkeys, []string{"subagents/agent-1", "subagents/agent-2"}) {
			t.Fatalf("ListSubkeys() = %#v, %v", subkeys, err)
		}
	}
}

func hasSessionIDs(entries []sdk.SessionStoreListEntry, want ...string) bool {
	if len(entries) != len(want) {
		return false
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		seen[entry.SessionID] = struct{}{}
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			return false
		}
	}
	return true
}
