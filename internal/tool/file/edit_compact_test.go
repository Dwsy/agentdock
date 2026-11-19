package file

import (
	"context"
	"testing"

	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

func TestEditKeepsFullResultInHistoryKey(t *testing.T) {
	runtime, _ := newFileTestService(t)
	result, err := runtime.editTest(context.Background(), map[string]any{
		"action":  "add",
		"path":    "compact.txt",
		"content": "new content\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	history, ok := result[FileEditHistoryKey].(toolcore.Result)
	if !ok {
		t.Fatalf("result has no %s history key: %#v", FileEditHistoryKey, result)
	}
	if _, hasDiff := history["diff_preview"]; !hasDiff {
		t.Fatalf("history key missing diff_preview: %#v", history)
	}
}

func TestCompactEditResultDropsDiffKeepsStatus(t *testing.T) {
	full := toolcore.Result{
		"action":        "replace",
		"path":          "x.txt",
		"changed":       true,
		"diff_preview":  "--- a/x.txt\n+++ b/x.txt\n@@ ...",
		"summary":       "updated x.txt",
		"files_changed": 1,
		"insertions":    2,
		"deletions":     1,
		"truncated":     true,
	}
	compact := CompactEditResult(full)
	if _, ok := compact["diff_preview"]; ok {
		t.Fatalf("compact result still carries diff_preview: %#v", compact)
	}
	if compact["success"] != true {
		t.Fatalf("compact result missing success=true: %#v", compact)
	}
	if compact["path"] != "x.txt" || compact["changed"] != true {
		t.Fatalf("compact result lost path/changed: %#v", compact)
	}
	if _, ok := compact["FileEditHistoryKey"]; ok {
		t.Fatal("compact result must not carry internal history key")
	}
}

func TestCompactEditResultPatchKeepsCounts(t *testing.T) {
	full := toolcore.Result{
		"action":         "patch",
		"diff_preview":   "big diff",
		"affected_files": []any{map[string]any{"path": "a.go", "operation": "update"}},
		"files_changed":  1,
		"insertions":     10,
		"deletions":      3,
	}
	compact := CompactEditResult(full)
	if compact["files_changed"] != 1 || compact["insertions"] != 10 || compact["deletions"] != 3 {
		t.Fatalf("patch compact lost counts: %#v", compact)
	}
	if _, ok := compact["affected_files"]; !ok {
		t.Fatalf("patch compact lost affected_files: %#v", compact)
	}
	if _, ok := compact["diff_preview"]; ok {
		t.Fatalf("patch compact still carries diff: %#v", compact)
	}
}
