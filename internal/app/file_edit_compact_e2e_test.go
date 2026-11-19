package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
)

// TestFileEditModelResultCompactButHistoryKeepsDiff verifies the split design:
// the model-facing Runtime.Call result is a small status summary with no diff,
// while the trace/file-history layer still records the diff via the internal key.
func TestFileEditModelResultCompactButHistoryKeepsDiff(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	root := t.TempDir()
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte("old line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := rt.Call(context.Background(), "file_edit", map[string]any{
		"action":  "replace",
		"path":    target,
		"old":     "old line\n",
		"content": "old line\nnew line\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Model-facing result: compact, no diff body, no internal history key.
	if result == nil {
		t.Fatal("nil result")
	}
	if _, ok := result["diff_preview"]; ok {
		t.Fatalf("model result still carries diff_preview: %#v", result)
	}
	if result["success"] != true {
		t.Fatalf("model result missing success=true: %#v", result)
	}
	for key := range result {
		if key == "_file_edit_history" {
			t.Fatal("model result leaked internal history key")
		}
	}

	// Trace / file-history still has the diff.
	traces, err := rt.RuntimeTraces(observability.TraceQuery{Tool: "file_edit"})
	if err != nil {
		t.Fatal(err)
	}
	records := traces["records"].([]observability.TraceRecord)
	if len(records) != 1 {
		t.Fatalf("trace records = %d, want 1", len(records))
	}
	changes := records[0].FileChanges
	if len(changes) != 1 {
		t.Fatalf("file changes = %d, want 1", len(changes))
	}
	if !strings.Contains(changes[0].Diff, "line") {
		t.Fatalf("file history lost the diff body: %#v", changes[0])
	}
}
