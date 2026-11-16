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

func patchTraceFixtures() map[string]string {
	return map[string]string{
		"envelope": "*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+one\n+two\n*** Update File: b.txt\n@@\n-old\n+new\n*** End Patch",
		"git":      "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n-old\n+one\n+two\ndiff --git a/b.txt b/b.txt\n--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-old\n+new\n",
		"unified":  "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n-old\n+one\n+two\n--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-old\n+new\n",
	}
}

func TestRuntimePatchTraceSummaries(t *testing.T) {
	for name, patch := range patchTraceFixtures() {
		t.Run(name, func(t *testing.T) {
			rt := newTraceTestRuntime(t, config.TraceModeMemory)
			root := t.TempDir()
			for _, name := range []string{"a.txt", "b.txt"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("old\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := rt.Call(context.Background(), "file_edit", map[string]any{
				"action": "patch", "workdir": root, "patch": patch,
			}); err != nil {
				t.Fatal(err)
			}
			result, err := rt.RuntimeTraces(observability.TraceQuery{Summary: true, Tool: "file_edit"})
			if err != nil {
				t.Fatal(err)
			}
			records := result["records"].([]observability.TraceRecord)
			if len(records) != 1 || len(records[0].FileChanges) != 2 {
				t.Fatalf("patch summary = %#v", records)
			}
			for _, change := range records[0].FileChanges {
				wantInsertions := 1
				if filepath.Base(change.Path) == "a.txt" {
					wantInsertions = 2
				}
				if change.Insertions != wantInsertions || change.Deletions != 1 || change.Diff != "" {
					t.Fatalf("summary change = %#v, want +%d/-1 without diff body", change, wantInsertions)
				}
			}
		})
	}
}

func TestRuntimePatchTraceCountsSurviveToolTruncation(t *testing.T) {
	for name, patch := range patchTraceFixtures() {
		t.Run(name, func(t *testing.T) {
			rt := newTraceTestRuntime(t, config.TraceModeMemory)
			root := t.TempDir()
			for _, name := range []string{"a.txt", "b.txt"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("old\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			patch = strings.Replace(patch, "+one\n", "+"+strings.Repeat("x", 70<<10)+"\n", 1)
			result, err := rt.Call(context.Background(), "file_edit", map[string]any{
				"action": "patch", "workdir": root, "patch": patch,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result["truncated"] != true {
				t.Fatal("large patch preview was not truncated")
			}
			summary, err := rt.RuntimeTraces(observability.TraceQuery{Summary: true, Tool: "file_edit"})
			if err != nil {
				t.Fatal(err)
			}
			records := summary["records"].([]observability.TraceRecord)
			if len(records) != 1 || len(records[0].FileChanges) != 2 {
				t.Fatalf("summary record count = %d, want one record with two files", len(records))
			}
			for _, change := range records[0].FileChanges {
				want := 1
				if filepath.Base(change.Path) == "a.txt" {
					want = 2
				}
				if change.Insertions != want || change.Deletions != 1 {
					t.Fatalf("%s counts = +%d/-%d, want +%d/-1", change.Path, change.Insertions, change.Deletions, want)
				}
			}
		})
	}
}

func TestTraceFileChangesAcceptsNativeAffectedFiles(t *testing.T) {
	type diffFile struct {
		Path   string `json:"path"`
		Status string `json:"status"`
	}
	for name, affected := range map[string]any{
		"envelope": []map[string]any{{"path": "/p/a.txt", "operation": "update"}},
		"git":      []diffFile{{Path: "/p/a.txt", Status: "modified"}},
		"wire":     []any{map[string]any{"path": "/p/a.txt", "operation": "update"}},
		"legacy":   []string{"/p/a.txt"},
	} {
		t.Run(name, func(t *testing.T) {
			changes := fileChangesFromResult(t, Result{
				"affected_files": affected,
				"diff_preview":   "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n",
			})
			if len(changes) != 1 || changes[0].Path != "/p/a.txt" || changes[0].Insertions != 1 || changes[0].Deletions != 1 {
				t.Fatalf("changes = %#v, want a.txt +1/-1", changes)
			}
		})
	}
}

func TestTraceFileChangesDeduplicatesAffectedFiles(t *testing.T) {
	changes := fileChangesFromResult(t, Result{
		"affected_files": []map[string]any{
			{"path": "/p/a.txt", "operation": "update"},
			{"path": "/p/a.txt", "operation": "update"},
			{"path": "/p/b.txt", "operation": "update"},
		},
		"diff_preview": "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n" +
			"--- a/b.txt\n+++ b/b.txt\n@@ -1 +1,2 @@\n-old\n+one\n+two\n",
		"insertions": 3, "deletions": 2,
	})
	if len(changes) != 2 || changes[0].Insertions != 1 || changes[1].Insertions != 2 {
		t.Fatalf("changes = %#v, want two distinct file summaries", changes)
	}
}

func TestTraceFileChangesPreservesExplicitZeroCounts(t *testing.T) {
	changes := fileChangesFromResult(t, Result{
		"path": "/p/a.txt", "insertions": 0, "deletions": 0,
		"diff_preview": "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n",
	})
	if len(changes) != 1 || changes[0].Insertions != 0 || changes[0].Deletions != 0 {
		t.Fatalf("changes = %#v, want explicit tool counts preserved", changes)
	}
}

func TestTraceAffectedFilesIgnoresInvalidEntries(t *testing.T) {
	for _, input := range []any{nil, "invalid", map[string]any{"path": "/a.txt"}, make(chan int),
		[]any{nil, 42, map[string]any{"path": ""}}} {
		if got := traceAffectedFiles(input, "patch"); len(got) != 0 {
			t.Fatalf("traceAffectedFiles(%#v) = %#v", input, got)
		}
	}
}

func TestLookupDiffStatsUsesMostSpecificPath(t *testing.T) {
	stats := map[string][2]int{"a.txt": {1, 1}, "src/a.txt": {2, 3}}
	for range 100 {
		if got, ok := lookupDiffStats(stats, "/project/src/a.txt"); !ok || got != [2]int{2, 3} {
			t.Fatalf("stats = %v, %t, want +2/-3", got, ok)
		}
	}
}

func TestTraceFileChangesCountsBeforePreviewTruncation(t *testing.T) {
	changes := fileChangesFromResult(t, Result{
		"affected_files": []any{
			map[string]any{"path": "/p/a.txt", "operation": "update"},
			map[string]any{"path": "/p/b.txt", "operation": "update"},
		},
		"diff_preview": "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-" + strings.Repeat("x", traceFileDiffBytes) + "\n+new\n" +
			"--- a/b.txt\n+++ b/b.txt\n@@ -1 +1,2 @@\n-old\n+one\n+two\n",
		"insertions": 3, "deletions": 2,
	})
	if len(changes) != 2 {
		t.Fatalf("changes = %#v", changes)
	}
	for i, want := range [][2]int{{1, 1}, {2, 1}} {
		change := changes[i]
		if change.Insertions != want[0] || change.Deletions != want[1] || !change.Truncated {
			t.Fatalf("change %d = %#v, want +%d/-%d with truncated preview", i, change, want[0], want[1])
		}
	}
}

func TestTraceFileChangesCombinesMoveSections(t *testing.T) {
	changes := fileChangesFromResult(t, Result{
		"affected_files": []any{
			map[string]any{"path": "/p/old.txt", "operation": "move", "move_to": "/p/new.txt"},
			map[string]any{"path": "/p/a.txt", "operation": "update"},
		},
		"diff_preview": "--- a/old.txt\n+++ b/old.txt\n@@ -1 +0,0 @@\n-old\n" +
			"--- a/new.txt\n+++ b/new.txt\n@@ -0,0 +1,2 @@\n+one\n+two\n" +
			"--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-x\n+y\n",
	})
	if len(changes) != 2 || changes[0].Insertions != 2 || changes[0].Deletions != 1 {
		t.Fatalf("changes = %#v, want move +2/-1", changes)
	}
}
