package observability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recordFixtures() []TraceRecord {
	started := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	return []TraceRecord{
		{Tool: "read_file", Source: "mcp", StartedAt: started, Success: true, Arguments: `{"path":"a.txt"}`, Result: "first"},
		{Tool: "write_file", Source: "internal", StartedAt: started.Add(time.Second), Success: false, ErrorCode: "INVALID_ARGUMENT", Arguments: `{"path":"b.txt"}`, Error: "boom"},
		{Tool: "read_file", Source: "internal", TaskID: "tsk_1", StartedAt: started.Add(2 * time.Second), Success: true, FileChanges: []FileChange{{Path: "a.txt", Action: "edit", Insertions: 1}}},
	}
}

func TestTraceStoreExportRecordsFiltersAndLimits(t *testing.T) {
	store := &TraceStore{mode: TraceModeMemory, max: 100}
	for _, record := range recordFixtures() {
		store.Record(record)
	}

	all := store.ExportRecords(TraceQuery{})
	if len(all) != 3 {
		t.Fatalf("export all = %d", len(all))
	}
	// 与 Query 一致：新到旧。
	if all[0].Tool != "read_file" || all[2].Tool != "read_file" || all[1].Tool != "write_file" {
		t.Fatalf("export order = %#v", all)
	}

	byTool := store.ExportRecords(TraceQuery{Tool: "read_file"})
	if len(byTool) != 2 {
		t.Fatalf("export by tool = %#v", byTool)
	}
	byStatus := store.ExportRecords(TraceQuery{Status: "error"})
	if len(byStatus) != 1 || byStatus[0].ErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("export by status = %#v", byStatus)
	}
	bySource := store.ExportRecords(TraceQuery{Source: "internal"})
	if len(bySource) != 2 {
		t.Fatalf("export by source = %#v", bySource)
	}
	byNeedle := store.ExportRecords(TraceQuery{Query: "tsk_1"})
	if len(byNeedle) != 1 || byNeedle[0].TaskID != "tsk_1" {
		t.Fatalf("export by needle = %#v", byNeedle)
	}
	limited := store.ExportRecords(TraceQuery{Limit: 2})
	if len(limited) != 2 {
		t.Fatalf("export limit = %#v", limited)
	}
	invalid := store.ExportRecords(TraceQuery{Status: "bogus"})
	if len(invalid) != 0 {
		t.Fatalf("export invalid status = %#v", invalid)
	}
}

func TestTraceStoreExportRecordsDefaultsToLargeLimit(t *testing.T) {
	store := &TraceStore{mode: TraceModeMemory, max: 3000}
	base := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := 0; i < traceExportLimit+10; i++ {
		store.Record(TraceRecord{Tool: "t", Source: "internal", StartedAt: base.Add(time.Duration(i) * time.Second), Success: true})
	}
	exported := store.ExportRecords(TraceQuery{})
	if len(exported) != traceExportLimit {
		t.Fatalf("export default limit = %d, want %d", len(exported), traceExportLimit)
	}
	if store.Query(TraceQuery{Limit: traceExportLimit}) == nil {
		t.Fatal("query should still cap at its own limit")
	}
}

func TestTraceStoreClearMemory(t *testing.T) {
	store := &TraceStore{mode: TraceModeMemory, max: 100}
	for _, record := range recordFixtures() {
		store.Record(record)
	}
	cleared, err := store.Clear()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 3 {
		t.Fatalf("cleared = %d, want 3", cleared)
	}
	if records := store.Query(TraceQuery{}); len(records) != 0 {
		t.Fatalf("query after clear = %#v", records)
	}
	if changes := store.FileChanges("", 0); len(changes) != 0 {
		t.Fatalf("file changes after clear = %#v", changes)
	}
	if store.Status().Count != 0 {
		t.Fatalf("status count = %d", store.Status().Count)
	}
	// 清空后仍可继续记录。
	store.Record(TraceRecord{Tool: "after", Source: "internal", StartedAt: time.Now().UTC(), Success: true})
	if records := store.Query(TraceQuery{}); len(records) != 1 || records[0].Tool != "after" {
		t.Fatalf("records after re-record = %#v", records)
	}
}

func TestTraceStoreClearRemovesDiskFiles(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeDisk, Root: root, BufferSize: 1, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range recordFixtures() {
		store.Record(record)
	}
	files, err := filepath.Glob(filepath.Join(root, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("expected flushed jsonl files, got %v (err=%v)", files, err)
	}

	cleared, err := store.Clear()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 3 {
		t.Fatalf("cleared = %d, want 3", cleared)
	}
	files, err = filepath.Glob(filepath.Join(root, "*.jsonl"))
	if err != nil || len(files) != 0 {
		t.Fatalf("jsonl files after clear = %v (err=%v)", files, err)
	}
	if records := store.Query(TraceQuery{}); len(records) != 0 {
		t.Fatalf("query after clear = %#v", records)
	}
	if changes := store.FileChanges("", 0); len(changes) != 0 {
		t.Fatalf("file changes after clear = %#v", changes)
	}

	// 写句柄已重置：清空后的新记录写进新文件，Close 也不报错。
	store.Record(TraceRecord{Tool: "after", Source: "internal", StartedAt: time.Now().UTC(), Success: true})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	files, err = filepath.Glob(filepath.Join(root, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("jsonl files after re-record = %v (err=%v)", files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"tool":"after"`) {
		t.Fatalf("rewritten file = %q", raw)
	}

	// 重启后只回放清空之后的记录。
	reopened, err := NewTraceStore(TraceOptions{Mode: TraceModeDisk, Root: root, BufferSize: 1, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if records := reopened.Query(TraceQuery{}); len(records) != 1 || records[0].Tool != "after" {
		t.Fatalf("records after restart = %#v", records)
	}
}

func TestTraceStoreClearOnNilStore(t *testing.T) {
	var store *TraceStore
	cleared, err := store.Clear()
	if err != nil || cleared != 0 {
		t.Fatalf("nil clear = %d, %v", cleared, err)
	}
	if records := store.ExportRecords(TraceQuery{}); len(records) != 0 {
		t.Fatalf("nil export = %#v", records)
	}
}
