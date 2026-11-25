package observability

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
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

func TestTraceStoreTaskIDPersistsAndSearchable(t *testing.T) {
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
		Tool: "read_file", Source: "mcp", Success: true, TaskID: "tsk_0123456789abcdef", StartedAt: time.Now(),
	})
	store.Record(TraceRecord{Tool: "read_file", Source: "mcp", Success: true, StartedAt: time.Now()})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewTraceStore(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	records := reloaded.Query(TraceQuery{})
	if len(records) != 2 || records[1].TaskID != "tsk_0123456789abcdef" {
		t.Fatalf("reloaded task ids = %#v", records)
	}
	// 任务 ID 必须参与自由文本搜索，否则磁盘证据无法从 UI 找回。
	if got := reloaded.Query(TraceQuery{Query: "tsk_0123456789abcdef"}); len(got) != 1 || got[0].TaskID != "tsk_0123456789abcdef" {
		t.Fatalf("task id search = %#v", got)
	}
	// 活动聚合必须精确匹配：无 task_id 的记录不能被空 ID 查询混入。
	if got := reloaded.TaskRecords("tsk_0123456789abcdef"); len(got) != 1 || got[0].TaskID != "tsk_0123456789abcdef" {
		t.Fatalf("task records = %#v", got)
	}
	if got := reloaded.TaskRecords(""); len(got) != 0 {
		t.Fatalf("empty task id matched records = %#v", got)
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

// summary 视图必须把 FileChanges 的 Diff 当正文清掉：列表页只用得上路径与
// 增删计数，保留 diff 会让 100 条 file_edit 记录多传数百 KB。
func TestFileChangeSummariesLeaveDiffsForDetail(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	changes := make([]FileChange, 10000)
	for i := range changes {
		changes[i] = FileChange{Path: "a.go", Action: "edit", Insertions: 3, Deletions: 2, Diff: "+private detail"}
	}
	store.Record(TraceRecord{Tool: "file_edit", StartedAt: time.Now(), FileChanges: changes})
	got, more := store.FileChangeSummaries("")
	if len(got) != 10000 || more || got[0].Insertions != 3 || got[0].Deletions != 2 || got[0].Diff != "" {
		t.Fatalf("summary count=%d, hasMore=%t, first=%#v", len(got), more, got[0])
	}
	store.Record(TraceRecord{Tool: "file_edit", StartedAt: time.Now(), FileChanges: []FileChange{{Path: "b.go"}}})
	got, more = store.FileChangeSummaries("")
	if len(got) != 10000 || !more || got[0].Path != "b.go" {
		t.Fatalf("over-cap summary count=%d, hasMore=%t, first=%#v", len(got), more, got[0])
	}
	legacy := store.FileChanges("a.go", 10000)
	if len(legacy) != traceQueryLimit || legacy[0].Diff != "+private detail" {
		t.Fatalf("legacy count=%d, first=%#v", len(legacy), legacy[0])
	}
}

func TestTraceStoreSummaryDropsFileChangeDiff(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	diff := strings.Repeat("+line\n", 1024)
	store.Record(TraceRecord{
		Tool: "file_edit", Source: "mcp", Success: true, StartedAt: time.Now(),
		FileChanges: []FileChange{{
			Path: "/tmp/project/a.go", Action: "replace",
			Insertions: 1024, Deletions: 3, Diff: diff, Truncated: true,
		}},
	})

	summary := store.Query(TraceQuery{Summary: true})
	if len(summary) != 1 {
		t.Fatalf("summary query = %d records", len(summary))
	}
	if len(summary[0].FileChanges) != 1 {
		t.Fatalf("summary lost file change metadata: %#v", summary[0].FileChanges)
	}
	change := summary[0].FileChanges[0]
	if change.Diff != "" {
		t.Fatalf("summary kept file diff (%d bytes)", len(change.Diff))
	}
	if change.Path != "/tmp/project/a.go" || change.Insertions != 1024 || change.Deletions != 3 || !change.Truncated {
		t.Fatalf("summary lost file change metadata: %#v", change)
	}

	// 摘要投影不能破坏保留窗口里的完整记录。
	full := store.Query(TraceQuery{})
	if len(full[0].FileChanges) != 1 || full[0].FileChanges[0].Diff != diff {
		t.Fatalf("full record was mutated: %d bytes", len(full[0].FileChanges[0].Diff))
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

func TestTraceStoreRingEvictionRecordByIDAndSearch(t *testing.T) {
	const capMax = 5
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: capMax})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var lastID uint64
	for i := 1; i <= 18; i++ {
		tool := "read_file"
		args := `{"path":"src/handler.go"}`
		if i == 17 {
			tool = "file_edit"
			args = `{"path":"中文路径/配置.md"}`
		}
		lastID = store.Record(TraceRecord{
			Tool:      tool,
			Source:    string(SourceMCP),
			Success:   true,
			Arguments: args,
			Result:    `{"ok":true}`,
			StartedAt: time.Now(),
		})
	}

	got := store.Query(TraceQuery{Limit: 20})
	if len(got) != capMax {
		t.Fatalf("Query len = %d, want %d", len(got), capMax)
	}
	// Evicted oldest records (1..13) must be absent; kept IDs must be 18..14 newest-first.
	if _, ok := store.RecordByID(13); ok {
		t.Fatal("evicted record ID 13 still accessible")
	}
	rec17, ok := store.RecordByID(lastID - 1)
	if !ok || rec17.Tool != "file_edit" {
		t.Fatalf("RecordByID(17) = (%v, %v)", rec17, ok)
	}

	if hits := store.Query(TraceQuery{Query: "HANDLER"}); len(hits) != capMax-1 {
		t.Fatalf("ASCII fold search hits = %d, want %d", len(hits), capMax-1)
	}
	if hits := store.Query(TraceQuery{Query: "配置.MD"}); len(hits) != 1 || hits[0].ID != lastID-1 {
		t.Fatalf("UTF-8 fold search hits = %#v", hits)
	}
}

// 游标分页必须按 ID 降序，不能沿用磁盘回放的 StartedAt 顺序：回放只保证
// 时间顺序，ID 才是唯一游标。这里故意把内存窗口重排成 StartedAt 顺序，
// 验证翻页不会跳记录，且无游标的旧路径保持原顺序。
func TestTraceStoreQueryPageCursorSortsByID(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		store.Record(TraceRecord{
			Tool: "exec_command", Source: string(SourceMCP), Success: true,
			// StartedAt 与 ID 反序，模拟并发写入后按时间回放。
			StartedAt: base.Add(time.Duration(5-i) * time.Second),
			Command:   &CommandTrace{Command: "echo hi"},
		})
	}
	store.mu.Lock()
	sort.Slice(store.records, func(i, j int) bool {
		return store.records[i].StartedAt.Before(store.records[j].StartedAt)
	})
	store.mu.Unlock()

	// 旧路径保持窗口现有顺序：不再额外排序。游标第一页即使不传 BeforeID，
	// 也必须按 ID 降序，否则从旧顺序取最小 ID 会漏掉其余记录。
	legacy := store.Query(TraceQuery{Limit: 5})
	var legacyIDs []uint64
	for _, record := range legacy {
		legacyIDs = append(legacyIDs, record.ID)
	}
	if !slices.Equal(legacyIDs, []uint64{1, 2, 3, 4, 5}) {
		t.Fatalf("legacy order = %v, want replay order [1 2 3 4 5]", legacyIDs)
	}

	page, hasMore := store.QueryPage(TraceQuery{Limit: 2, Summary: true, Cursor: true})
	if !hasMore || len(page) != 2 || page[0].ID != 5 || page[1].ID != 4 {
		t.Fatalf("first page = %#v has_more=%v", page, hasMore)
	}
	if page[0].Command == nil || page[0].Command.Summary != "echo hi" {
		t.Fatalf("summary lost command metadata: %#v", page[0].Command)
	}

	page, hasMore = store.QueryPage(TraceQuery{Limit: 2, Summary: true, Cursor: true, BeforeID: 4})
	if !hasMore || len(page) != 2 || page[0].ID != 3 || page[1].ID != 2 {
		t.Fatalf("second page = %#v has_more=%v", page, hasMore)
	}

	page, hasMore = store.QueryPage(TraceQuery{Limit: 2, Summary: true, Cursor: true, BeforeID: 2})
	if hasMore || len(page) != 1 || page[0].ID != 1 {
		t.Fatalf("last page = %#v has_more=%v", page, hasMore)
	}

	// 过滤条件参与游标判断：只剩 ID 3 匹配时，has_more 必须为 false。
	store.mu.Lock()
	store.records[2].Tool = "read_file"
	store.mu.Unlock()
	page, hasMore = store.QueryPage(TraceQuery{Limit: 2, Tool: "read_file", Cursor: true, BeforeID: 4})
	if hasMore || len(page) != 1 || page[0].ID != 3 {
		t.Fatalf("filtered page = %#v has_more=%v", page, hasMore)
	}

	if page, hasMore := store.QueryPage(TraceQuery{Cursor: true, BeforeID: 4, Status: "bogus"}); page != nil || hasMore {
		t.Fatalf("invalid status = %#v has_more=%v", page, hasMore)
	}
}

func TestTraceStoreQueryPageFiltersOutcomeBeforePagination(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, record := range []TraceRecord{
		{Success: true, Command: &CommandTrace{Status: "running", ExitCode: new(2)}},
		{Success: true, Command: &CommandTrace{Status: "running"}},
		{Success: true, Command: &CommandTrace{Status: "running", TimedOut: true, CommandOK: new(true)}},
		{Success: true, Command: &CommandTrace{Status: "running", CommandOK: new(true), ExitCode: new(2)}},
		{Success: false, Command: &CommandTrace{CommandOK: new(true)}},
		{Success: true, Command: &CommandTrace{Status: "running", ExitCode: new(0)}},
	} {
		record.Tool = "exec_command"
		record.StartedAt = time.Now()
		store.Record(record)
	}
	check := func(query TraceQuery, wantIDs []uint64, wantMore bool) {
		t.Helper()
		records, hasMore := store.QueryPage(query)
		var ids []uint64
		for _, record := range records {
			ids = append(ids, record.ID)
		}
		if !slices.Equal(ids, wantIDs) || hasMore != wantMore {
			t.Fatalf("query %+v: ids = %v, has_more = %t, want %v, %t", query, ids, hasMore, wantIDs, wantMore)
		}
	}
	for _, cursor := range []bool{false, true} {
		check(TraceQuery{Outcome: " ERROR ", Limit: 2, Cursor: cursor, Summary: true}, []uint64{5, 3}, true)
		check(TraceQuery{Outcome: "success", Limit: 2, Cursor: cursor}, []uint64{6, 4}, false)
		check(TraceQuery{Status: "error", Limit: 2, Cursor: cursor}, []uint64{5}, false)
		if page, hasMore := store.QueryPage(TraceQuery{Outcome: "running", Cursor: cursor}); page != nil || hasMore {
			t.Fatalf("cursor=%t: invalid outcome returned %d records, has_more=%t", cursor, len(page), hasMore)
		}
	}
	check(TraceQuery{Outcome: "error", Limit: 2, Cursor: true, BeforeID: 3}, []uint64{1}, false)
}

// 无游标请求也要能报告 has_more，供首屏判断是否显示“加载更早”。
func TestTraceStoreQueryPageWithoutCursorReportsHasMore(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for i := 0; i < 3; i++ {
		store.Record(TraceRecord{Tool: "read_file", Source: string(SourceMCP), Success: true, StartedAt: time.Now()})
	}

	page, hasMore := store.QueryPage(TraceQuery{Limit: 2})
	if !hasMore || len(page) != 2 || page[0].ID != 3 || page[1].ID != 2 {
		t.Fatalf("page = %#v has_more=%v", page, hasMore)
	}
	page, hasMore = store.QueryPage(TraceQuery{Limit: 3})
	if hasMore || len(page) != 3 {
		t.Fatalf("full page = %#v has_more=%v", page, hasMore)
	}
}
