package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
)

func TestRuntimeTraceDetailFullCopy(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeDisk)
	body := strings.Repeat("x", observability.TracePreviewBytes*2)
	rt.traces.Record(observability.TraceRecord{
		Tool:      "read_file",
		Success:   true,
		StartedAt: time.Now(),
		Arguments: `{"body":"` + body + `"}`,
	})
	rt.traces.Record(observability.TraceRecord{
		Tool:      "read_file",
		Success:   true,
		StartedAt: time.Now(),
		Arguments: `{"path":"a.go"}`,
	})
	records := rt.traces.Query(observability.TraceQuery{})
	if len(records) != 2 {
		t.Fatalf("records = %d", len(records))
	}
	// Query 返回新到旧：records[0] 是小 payload 记录。
	smallID, truncatedID := records[0].ID, records[1].ID

	detail, err := rt.RuntimeTraceDetail(truncatedID, true)
	if err != nil {
		t.Fatal(err)
	}
	if detail["full_available"] != true {
		t.Fatalf("full_available = %#v", detail["full_available"])
	}
	full, ok := detail["full"].(observability.TraceFullRecord)
	if !ok {
		t.Fatalf("full = %#v", detail["full"])
	}
	if len(full.Arguments) <= observability.TracePreviewBytes {
		t.Fatalf("full arguments = %d bytes, want beyond preview", len(full.Arguments))
	}

	smallDetail, err := rt.RuntimeTraceDetail(smallID, true)
	if err != nil {
		t.Fatal(err)
	}
	if smallDetail["full_available"] != false || smallDetail["full"] != nil {
		t.Fatalf("small record full = %#v", smallDetail)
	}

	previewDetail, err := rt.RuntimeTraceDetail(truncatedID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := previewDetail["full"]; has {
		t.Fatal("full=false must not attach full payload")
	}
}

func TestRuntimeFileDiffSidecarForStandaloneEdits(t *testing.T) {
	for _, action := range []string{"add", "replace"} {
		t.Run(action, func(t *testing.T) {
			rt := newTraceTestRuntime(t, config.TraceModeDisk)
			target := filepath.Join(t.TempDir(), "notes.txt")
			const secret = "test-fixture-credential"
			old := "{\"token\":\"" + secret + "\"}\nold\n"
			if err := os.WriteFile(target, []byte(old), 0o600); err != nil {
				t.Fatal(err)
			}
			largeLine := strings.Repeat("x", 70<<10)
			content := largeLine + "\nlast added line\n"
			args := map[string]any{"action": action, "path": target}
			if action == "add" {
				args["content"] = content
				args["overwrite"] = true
			} else {
				args["old"] = "old\n"
				args["new"] = content
				args["max_diff_bytes"] = 16
			}
			result, err := rt.Call(context.Background(), "file_edit", args)
			if err != nil || result["truncated"] != true || result["diff_preview"] != nil || result["file_diff"] != nil {
				t.Fatalf("unexpected model result: %#v, error %v", result, err)
			}
			record := rt.traces.Query(observability.TraceQuery{Tool: "file_edit"})[0]
			if len(record.FileChanges) != 1 {
				t.Fatalf("file changes count = %d", len(record.FileChanges))
			}
			change := record.FileChanges[0]
			wantDeletions := 1
			if action == "add" {
				wantDeletions = 2
			}
			if !change.Truncated || len(change.Diff) > traceFileDiffBytes || change.Insertions != 2 || change.Deletions != wantDeletions {
				t.Fatalf("preview: bytes=%d, truncated=%v, counts +%d/-%d", len(change.Diff), change.Truncated, change.Insertions, change.Deletions)
			}
			if action == "replace" && len(change.Diff) != 16 {
				t.Fatalf("replace preview bytes = %d, want 16", len(change.Diff))
			}
			// Changing the file again cannot change the captured invocation's diff.
			if err := os.WriteFile(target, []byte("unrelated later content\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			detail, err := rt.RuntimeTraceDetail(record.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			full, ok := detail["full"].(observability.TraceFullRecord)
			if !ok || detail["full_available"] != true || full.FileDiffTruncated || !strings.Contains(full.FileDiff, "-old\n+"+largeLine+"\n+last added line\n") {
				t.Fatalf("missing original diff: available=%v, bytes=%d, truncated=%v", ok, len(full.FileDiff), full.FileDiffTruncated)
			}
			if !strings.Contains(full.FileDiff, `"token":"***"`) {
				t.Fatal("full file diff lost its redacted context")
			}
			if strings.Contains(full.FileDiff, secret) || strings.Contains(full.Result, secret) || strings.Contains(record.Result, secret) || strings.Contains(change.Diff, secret) {
				t.Fatal("trace file diff or preview exposed a credential")
			}
			if strings.Contains(full.Result, "last added line") {
				t.Fatal("uncapped diff entered the generic result JSON")
			}
			wire, err := json.Marshal(detail)
			if err != nil || !strings.Contains(string(wire), `"file_diff":`) || strings.Contains(string(wire), `"file_diff_truncated":`) {
				t.Fatalf("unexpected file diff fields in detail JSON, error %v", err)
			}
			for _, format := range []string{"json", "csv"} {
				export, _, _, err := rt.RuntimeTracesExport(format, "", "file_edit", "", "", 0)
				if err != nil || strings.Contains(string(export), "file_diff") || strings.Contains(string(export), "last added line") {
					t.Fatalf("%s export exposed the sidecar file diff, error %v", format, err)
				}
			}
		})
	}
}

func TestRuntimeFileDiffSidecarPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    config.TraceMode
		dryRun  bool
		wantErr bool
	}{
		{name: "disk", mode: config.TraceModeDisk},
		{name: "memory", mode: config.TraceModeMemory},
		{name: "dry run", mode: config.TraceModeDisk, dryRun: true},
		{name: "failed apply", mode: config.TraceModeDisk, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newTraceTestRuntime(t, tc.mode)
			root := t.TempDir()
			target := filepath.Join(root, "notes.txt")
			if err := os.WriteFile(target, []byte("old\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{
				"action": "replace", "path": target, "old": "old", "new": "new", "max_diff_bytes": 1, "dry_run": tc.dryRun,
			}
			if tc.wantErr {
				args = map[string]any{
					"action": "patch", "workdir": root, "max_diff_bytes": 1,
					"patch": "--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-wrong context\n+new\n",
				}
			}
			if _, err := rt.Call(context.Background(), "file_edit", args); (err != nil) != tc.wantErr {
				t.Fatalf("tool call error = %v, want error=%v", err, tc.wantErr)
			}
			record := rt.traces.Query(observability.TraceQuery{Tool: "file_edit"})[0]
			detail, err := rt.RuntimeTraceDetail(record.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			wantFull := tc.mode == config.TraceModeDisk && !tc.dryRun && !tc.wantErr
			if detail["full_available"] != wantFull {
				t.Fatalf("full_available = %v, want %v", detail["full_available"], wantFull)
			}
			if wantFull && !strings.HasSuffix(detail["full"].(observability.TraceFullRecord).FileDiff, "-old\n+new\n") {
				t.Fatal("small diff lost its changes after truncation by the tool")
			}
			if (tc.dryRun || tc.wantErr) && len(record.FileChanges) != 0 {
				t.Fatal("dry-run or failed edit entered file history")
			}
		})
	}
}
