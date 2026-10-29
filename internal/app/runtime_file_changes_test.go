package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/observability"
)

func fileChangesFromResult(t *testing.T, result Result) []observability.FileChange {
	t.Helper()
	return traceFileChanges("file_edit", map[string]any{"action": "patch"}, &result, true)
}

func TestTraceFileChangesParsesStructuredPatchEnvelope(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a.txt b/a.txt", "--- a/a.txt", "+++ b/a.txt", "@@ -1 +1 @@", "-old", "+new",
		"diff --git a/b.txt b/b.txt", "--- a/b.txt", "+++ b/b.txt", "@@ -1 +1 @@", "-x", "+y",
	}, "\n")
	result := Result{
		"action": "patch",
		"affected_files": []any{
			map[string]any{"path": "/p/a.txt", "operation": "update"},
			map[string]any{"path": "/p/old.txt", "operation": "move", "move_to": "/p/new.txt"},
			map[string]any{"path": "/p/created.txt", "operation": "add"},
			map[string]any{"path": "/p/gone.txt", "operation": "delete"},
		},
		"diff_preview": diff,
		"insertions":   99,
		"deletions":    99,
	}
	changes := fileChangesFromResult(t, result)
	if len(changes) != 4 {
		t.Fatalf("changes = %d, want 4: %#v", len(changes), changes)
	}
	wantActions := map[string]string{
		"/p/a.txt":       "edit",
		"/p/old.txt":     "move",
		"/p/created.txt": "create",
		"/p/gone.txt":    "delete",
	}
	for _, change := range changes {
		if change.Action != wantActions[change.Path] {
			t.Fatalf("action for %s = %q, want %q", change.Path, change.Action, wantActions[change.Path])
		}
	}
	var move *observability.FileChange
	for i := range changes {
		if changes[i].Action == "move" {
			move = &changes[i]
		}
	}
	if move == nil || move.MoveTo != "/p/new.txt" {
		t.Fatalf("move entry = %#v", move)
	}
	// 多文件 patch 的 +/- 必须按分节统计，不能把调用级总数复制给每个文件。
	for _, change := range changes {
		if change.Insertions == 99 || change.Deletions == 99 {
			t.Fatalf("per-file stats missing: %#v", change)
		}
	}
	if changes[0].Insertions != 1 || changes[0].Deletions != 1 {
		t.Fatalf("a.txt stats = +%d/-%d, want +1/-1", changes[0].Insertions, changes[0].Deletions)
	}
}

func TestTraceFileChangesParsesUnifiedDiffEntries(t *testing.T) {
	result := Result{
		"action": "patch",
		"affected_files": []any{
			map[string]any{"path": "b/new.txt", "status": "added", "binary": false},
		},
		"diff_preview": "diff --git a/new.txt b/new.txt\nnew file mode 100644\n--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1 @@\n+hi\n",
	}
	changes := fileChangesFromResult(t, result)
	if len(changes) != 1 || changes[0].Action != "create" {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestTraceFileChangesSkipsDryRunAndNoChange(t *testing.T) {
	dryRun := Result{"action": "replace", "path": "/p/a.txt", "dry_run": true, "changed": true}
	if got := traceFileChanges("file_edit", nil, &dryRun, true); got != nil {
		t.Fatalf("dry run produced history: %#v", got)
	}
	noChange := Result{"action": "replace", "path": "/p/a.txt", "changed": false}
	if got := traceFileChanges("file_edit", nil, &noChange, true); got != nil {
		t.Fatalf("unchanged call produced history: %#v", got)
	}
	failed := Result{"action": "replace", "path": "/p/a.txt"}
	if got := traceFileChanges("file_edit", nil, &failed, false); got != nil {
		t.Fatalf("failed call produced history: %#v", got)
	}
}

func TestTraceFileChangesMoveRecordsDestination(t *testing.T) {
	result := Result{"action": "move", "path": "/p/old.txt", "new_path": "/p/new.txt", "changed": true}
	changes := fileChangesFromResult(t, result)
	if len(changes) != 1 || changes[0].Action != "move" || changes[0].MoveTo != "/p/new.txt" {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestNormalizeFileAction(t *testing.T) {
	for raw, want := range map[string]string{
		"add": "create", "added": "create",
		"replace": "edit", "patch": "edit", "update": "edit", "modified": "edit",
		"delete": "delete", "deleted": "delete",
		"move": "move", "rename": "move", "": "edit",
	} {
		if got := normalizeFileAction(raw); got != want {
			t.Fatalf("normalizeFileAction(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestTraceProjectRootFindsMarkers(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(project, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(nested, "x.go")
	if got := traceProjectRoot(file); got != project {
		t.Fatalf("project root = %q, want %q", got, project)
	}
	// 无标记目录回退父目录。
	plain := t.TempDir()
	plainFile := filepath.Join(plain, "y.txt")
	if got := traceProjectRoot(plainFile); got != plain {
		t.Fatalf("fallback root = %q, want %q", got, plain)
	}
}
