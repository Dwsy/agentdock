package app

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

func exportFixtureRecords() []observability.TraceRecord {
	started := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	return []observability.TraceRecord{
		{
			Tool: "read_file", Source: "internal", UserAgent: "ua/1", TraceID: "tr-1",
			StartedAt: started, DurationMS: 12.5, Success: true,
			Arguments: `a,"b"` + "\nsecond line", Result: "x,y",
		},
		{
			Tool: "write_file", Source: "mcp", StartedAt: started.Add(time.Second), Success: false,
			ErrorCode: "INVALID_ARGUMENT", Arguments: `{"plain":true}`, Result: "", Error: "boom",
		},
	}
}

func TestRuntimeTracesExportJSONShape(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	for _, record := range exportFixtureRecords() {
		rt.traces.Record(record)
	}

	body, contentType, filename, err := rt.RuntimeTracesExport("json", "", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" || !strings.HasSuffix(filename, ".json") {
		t.Fatalf("meta = %q %q", contentType, filename)
	}
	if !strings.Contains(string(body), "\n  {") {
		t.Fatalf("json body must be indented with 2 spaces: %q", body)
	}
	var records []observability.TraceRecord
	if err := json.Unmarshal(body, &records); err != nil {
		t.Fatalf("export body is not a records array: %v\n%s", err, body)
	}
	if len(records) != 2 || records[0].Tool != "write_file" || records[1].Tool != "read_file" {
		t.Fatalf("exported records = %#v", records)
	}

	// limit 生效，过滤语义与查询一致。
	limited, _, _, err := rt.RuntimeTracesExport("json", "", "write_file", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(limited, &records); err != nil || len(records) != 1 || records[0].Tool != "write_file" {
		t.Fatalf("filtered export = %s (err=%v)", limited, err)
	}
}

func TestRuntimeTracesExportJSONEmptyArrayWhenDisabled(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeOff)
	body, contentType, _, err := rt.RuntimeTracesExport("json", "", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "[]" || contentType != "application/json" {
		t.Fatalf("disabled export = %q %q", body, contentType)
	}
}

func TestRuntimeTracesExportCSVHeaderAndEscaping(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	for _, record := range exportFixtureRecords() {
		rt.traces.Record(record)
	}

	body, contentType, filename, err := rt.RuntimeTracesExport("csv", "", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/csv" || !strings.HasSuffix(filename, ".csv") {
		t.Fatalf("meta = %q %q", contentType, filename)
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("csv line count = %d\n%s", len(lines), body)
	}
	header := "id,started_at,tool,source,user_agent,trace_id,duration_ms,success,error_code,arguments,result,error"
	if lines[0] != header {
		t.Fatalf("csv header = %q", lines[0])
	}
	// 引号翻倍、含逗号/引号的字段加引号；换行转成字面 \n 保持单行。
	// 记录顺序与 Query 一致：新到旧。
	wantNewest := `2,2024-01-02T03:04:06Z,write_file,mcp,,,0,false,INVALID_ARGUMENT,"{""plain"":true}",,boom`
	if lines[1] != wantNewest {
		t.Fatalf("csv row 1 = %q\nwant     %q", lines[1], wantNewest)
	}
	wantOldest := `1,2024-01-02T03:04:05Z,read_file,internal,ua/1,tr-1,12.5,true,,"a,""b""\nsecond line","x,y",`
	if lines[2] != wantOldest {
		t.Fatalf("csv row 2 = %q\nwant     %q", lines[2], wantOldest)
	}
}

func TestRuntimeTracesExportRejectsInvalidFormat(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	for _, format := range []string{"xml", "JSON5", "yml"} {
		_, _, _, err := rt.RuntimeTracesExport(format, "", "", "", "", 0)
		var toolErr *toolcore.ToolError
		if err == nil {
			t.Fatalf("format %q: expected error", format)
		}
		if !errors.As(err, &toolErr) || toolErr.Code != "INVALID_ARGUMENT" || toolErr.Category != "validation" {
			t.Fatalf("format %q error = %#v", format, err)
		}
	}
}

func TestRuntimeTracesExportDefaultsFormatToJSON(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	body, contentType, _, err := rt.RuntimeTracesExport("", "", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "[]" || contentType != "application/json" {
		t.Fatalf("default export = %q %q", body, contentType)
	}
}

func TestRuntimeTracesClearMemory(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	for _, record := range exportFixtureRecords() {
		rt.traces.Record(record)
	}
	result, err := rt.RuntimeTracesClear()
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["source"] != runtimeAPISource || result["action"] != "clear" || result["cleared"] != 2 {
		t.Fatalf("clear result = %#v", result)
	}
	traces, err := rt.RuntimeTraces("", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if traces["count"] != 0 {
		t.Fatalf("traces after clear = %#v", traces)
	}
	changes, err := rt.RuntimeFileChanges("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if changes["count"] != 0 {
		t.Fatalf("file changes after clear = %#v", changes)
	}
}

func TestRuntimeTracesClearDiskRemovesFiles(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeDisk)
	for _, record := range exportFixtureRecords() {
		rt.traces.Record(record)
	}
	recorded := rt.traces.Query(observability.TraceQuery{Limit: 10})
	if len(recorded) != 2 {
		t.Fatalf("recorded = %#v", recorded)
	}

	root, err := filepath.EvalSymlinks(rt.traces.Status().Root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("expected flushed trace files, got %v (err=%v)", files, err)
	}

	result, err := rt.RuntimeTracesClear()
	if err != nil {
		t.Fatal(err)
	}
	if result["cleared"] != 2 {
		t.Fatalf("clear result = %#v", result)
	}
	files, err = filepath.Glob(filepath.Join(root, "*.jsonl"))
	if err != nil || len(files) != 0 {
		t.Fatalf("trace files after clear = %v (err=%v)", files, err)
	}
	traces, err := rt.RuntimeTraces("", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if traces["count"] != 0 {
		t.Fatalf("traces after clear = %#v", traces)
	}
	changes, err := rt.RuntimeFileChanges("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if changes["count"] != 0 {
		t.Fatalf("file changes after clear = %#v", changes)
	}
}

func TestRuntimeTracesClearDisabled(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeOff)
	result, err := rt.RuntimeTracesClear()
	if err != nil {
		t.Fatal(err)
	}
	if result["cleared"] != 0 || result["ok"] != true {
		t.Fatalf("clear result = %#v", result)
	}
}
