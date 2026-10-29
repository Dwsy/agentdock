package observability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTraceStoreMemoryRecordsAndFilters(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	store.Record(TraceRecord{Tool: "read_file", Source: "mcp", Success: true, Arguments: `{"path":"a.go"}`, Result: `{"ok":true}`, StartedAt: time.Now()})
	store.Record(TraceRecord{Tool: "exec_command", Source: "nexus", Success: false, ErrorCode: "DENIED", Error: "blocked by policy", StartedAt: time.Now()})

	if got := store.Query(TraceQuery{}); len(got) != 2 {
		t.Fatalf("Query() returned %d records, want 2", len(got))
	}
	if got := store.Query(TraceQuery{Tool: "read_file"}); len(got) != 1 || got[0].Tool != "read_file" {
		t.Fatalf("tool filter = %#v", got)
	}
	if got := store.Query(TraceQuery{Source: "nexus"}); len(got) != 1 || got[0].Source != "nexus" {
		t.Fatalf("source filter = %#v", got)
	}
	if got := store.Query(TraceQuery{Status: "error"}); len(got) != 1 || got[0].Success {
		t.Fatalf("status filter = %#v", got)
	}
	if got := store.Query(TraceQuery{Query: "policy"}); len(got) != 1 || got[0].Tool != "exec_command" {
		t.Fatalf("text filter = %#v", got)
	}
	if got := store.Query(TraceQuery{Query: "a.go"}); len(got) != 1 || got[0].Tool != "read_file" {
		t.Fatalf("argument filter = %#v", got)
	}
	if names := store.ToolNames(); len(names) != 2 || names[0] != "exec_command" || names[1] != "read_file" {
		t.Fatalf("ToolNames() = %#v", names)
	}
}

func TestTraceStoreTruncatesAndRedacts(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	store.Record(TraceRecord{
		Tool:      "read_file",
		Success:   true,
		Arguments: `{"token":"supersecret","body":"` + strings.Repeat("x", TracePreviewBytes*2) + `"}`,
	})

	record := store.Query(TraceQuery{})[0]
	if !record.Truncated {
		t.Fatal("oversized payload was not marked truncated")
	}
	if len(record.Arguments) > TracePreviewBytes+len("…(truncated)") {
		t.Fatalf("preview length = %d", len(record.Arguments))
	}
	if strings.Contains(record.Arguments, "supersecret") {
		t.Fatalf("secret leaked into trace preview: %s", record.Arguments)
	}
}

func TestTraceStoreDiskPersistsAndReloads(t *testing.T) {
	root := t.TempDir()
	options := TraceOptions{
		Mode:          TraceModeDisk,
		Root:          root,
		Retention:     24 * time.Hour,
		BufferSize:    1,
		FlushInterval: 20 * time.Millisecond,
		MaxRecords:    100,
	}
	store, err := NewTraceStore(options)
	if err != nil {
		t.Fatal(err)
	}
	store.Record(TraceRecord{
		Tool: "file_edit", Source: "mcp", Success: true, Arguments: `{"action":"add"}`,
		UserAgent:   "ChatGPT/1.0 (+macOS)",
		FileChanges: []FileChange{{Path: "/tmp/a.txt", Action: "add", Insertions: 1, Diff: "@@ -0,0 +1 @@"}},
		StartedAt:   time.Now(),
	})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("trace files = %v, err = %v", entries, err)
	}

	reloaded, err := NewTraceStore(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	records := reloaded.Query(TraceQuery{})
	if len(records) != 1 || records[0].Tool != "file_edit" || records[0].Arguments != `{"action":"add"}` {
		t.Fatalf("reloaded records = %#v", records)
	}
	if changes := reloaded.FileChanges("/tmp/a.txt", 10); len(changes) != 1 || changes[0].Diff == "" {
		t.Fatalf("reloaded file changes = %#v", changes)
	}
	if records[0].UserAgent != "ChatGPT/1.0 (+macOS)" {
		t.Fatalf("reloaded user agent = %q", records[0].UserAgent)
	}
	if got := reloaded.Query(TraceQuery{Query: "chatgpt"}); len(got) != 1 {
		t.Fatalf("UA search = %#v", got)
	}
	// 重启后 ID 必须继续递增，不能覆盖已落盘记录的排序。
	reloaded.Record(TraceRecord{Tool: "read_file", Success: true, StartedAt: time.Now()})
	next := reloaded.Query(TraceQuery{})
	if next[0].ID <= records[0].ID {
		t.Fatalf("reloaded next id = %d, previous = %d", next[0].ID, records[0].ID)
	}
}

func TestTraceStoreCleanupRemovesExpiredFiles(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "2020-01-01.jsonl")
	if err := os.WriteFile(stale, []byte(`{"id":1,"tool":"old"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewTraceStore(TraceOptions{
		Mode:       TraceModeDisk,
		Root:       root,
		Retention:  24 * time.Hour,
		MaxRecords: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expired trace file still exists: %v", err)
	}
}

func TestTraceStoreOffReturnsNil(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeOff})
	if err != nil {
		t.Fatal(err)
	}
	if store != nil {
		t.Fatal("off mode must not allocate a store")
	}
	var nilStore *TraceStore
	nilStore.Record(TraceRecord{Tool: "read_file"})
	if got := nilStore.Query(TraceQuery{}); got != nil {
		t.Fatalf("nil store query = %#v", got)
	}
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nil store close = %v", err)
	}
}

func TestTraceStoreFileChangesProjection(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	store.Record(TraceRecord{
		Tool: "file_edit", Source: "mcp", Success: true, StartedAt: time.Now(),
		FileChanges: []FileChange{
			{Path: "/tmp/project/a.go", Action: "replace", Insertions: 2, Deletions: 1, Diff: "@@ -1 +1 @@"},
			{Path: "/tmp/project/sub/b.go", Action: "add", Insertions: 5},
		},
	})
	store.Record(TraceRecord{Tool: "read_file", Success: true, StartedAt: time.Now()})
	store.Record(TraceRecord{
		Tool: "file_edit", Source: "internal", Success: true, StartedAt: time.Now(),
		FileChanges: []FileChange{{Path: "/tmp/other/c.go", Action: "delete"}},
	})

	all := store.FileChanges("", 10)
	if len(all) != 3 {
		t.Fatalf("FileChanges() = %d entries", len(all))
	}
	if all[0].Path != "/tmp/other/c.go" {
		t.Fatalf("newest entry = %#v", all[0])
	}
	if all[1].Path != "/tmp/project/sub/b.go" || all[1].Action != "add" {
		t.Fatalf("second entry = %#v", all[1])
	}

	filtered := store.FileChanges("PROJECT/A", 10)
	if len(filtered) != 1 || filtered[0].Path != "/tmp/project/a.go" {
		t.Fatalf("filtered = %#v", filtered)
	}
	if limited := store.FileChanges("", 1); len(limited) != 1 {
		t.Fatalf("limit not applied: %#v", limited)
	}
}
