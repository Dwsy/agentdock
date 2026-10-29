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
	store.Record(TraceRecord{Tool: "exec_command", Source: "mcp", Success: true, Arguments: `{"cmd":"ls"}`, StartedAt: time.Now()})
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
	if len(records) != 1 || records[0].Tool != "exec_command" || records[0].Arguments != `{"cmd":"ls"}` {
		t.Fatalf("reloaded records = %#v", records)
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
