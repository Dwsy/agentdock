package file

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseDiffFilesGitStyle(t *testing.T) {
	diff := "" +
		"diff --git a/a.txt b/a.txt\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/a.txt\n" +
		"@@ -0,0 +1 @@\n" +
		"+hi\n"
	files := parseDiffFiles(diff)
	if len(files) != 1 || files[0].Path != "a.txt" || files[0].Status != "added" {
		t.Fatalf("files = %#v", files)
	}
}

// git apply 接受纯 unified diff；没有 diff --git 行时也要解析出文件列表，
// 否则历史层拿不到 affected_files 会整条丢弃变更。
func TestParseDiffFilesUnifiedDiffFallback(t *testing.T) {
	diff := "" +
		"--- a/src/x.go\n" +
		"+++ b/src/x.go\n" +
		"@@ -1,2 +1,2 @@\n" +
		" ctx\n" +
		"-old\n" +
		"+new\n" +
		"--- /dev/null\n" +
		"+++ b/src/y.go\n" +
		"@@ -0,0 +1 @@\n" +
		"+created\n" +
		"--- a/gone.txt\n" +
		"+++ /dev/null\n" +
		"@@ -1 +0,0 @@\n" +
		"-bye\n"
	files := parseDiffFiles(diff)
	if len(files) != 3 {
		t.Fatalf("files = %#v", files)
	}
	if files[0].Path != "src/x.go" || files[0].Status != "modified" {
		t.Fatalf("x.go = %#v", files[0])
	}
	if files[1].Path != "src/y.go" || files[1].Status != "added" {
		t.Fatalf("y.go = %#v", files[1])
	}
	if files[2].Path != "gone.txt" || files[2].Status != "deleted" {
		t.Fatalf("gone.txt = %#v", files[2])
	}
}

// 删除 "-- x" 渲染为 "--- x"：内容行不能被当成文件头新增一条 affected_files。
func TestParseDiffFilesIgnoresHeaderLikeContent(t *testing.T) {
	diff := "" +
		"--- a/a.txt\n" +
		"+++ b/a.txt\n" +
		"@@ -1,3 +1,2 @@\n" +
		" keep\n" +
		"--- dashes\n" +
		"+++ pluses\n"
	files := parseDiffFiles(diff)
	if len(files) != 1 || files[0].Path != "a.txt" {
		t.Fatalf("files = %#v", files)
	}
}

func TestParseDiffFilesPreservesGitPaths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch string
		want  gitDiffFile
	}{
		{
			name:  "space with CRLF and timestamps",
			patch: "diff --git a/my note.txt b/my note.txt\r\n--- a/my note.txt\tdate\r\n+++ b/my note.txt\tdate\r\n@@ -1 +1 @@\r\n--- a/not-a-header\r\n+++ b/not-a-header\r\n",
			want:  gitDiffFile{Path: "my note.txt", Status: "modified", Insertions: 1, Deletions: 1},
		},
		{
			name:  "quoted UTF-8 with nested b prefix",
			patch: "diff --git \"a/b/\\344\\270\\255.txt\" \"b/b/\\344\\270\\255.txt\"\n--- \"a/b/\\344\\270\\255.txt\"\n+++ \"b/b/\\344\\270\\255.txt\"\n@@ -1 +1 @@\n-old\n+new\n",
			want:  gitDiffFile{Path: "b/中.txt", Status: "modified", Insertions: 1, Deletions: 1},
		},
		{
			name:  "mode-only path containing separator text",
			patch: "diff --git a/dir b/note name.txt b/dir b/note name.txt\nold mode 100644\nnew mode 100755\n",
			want:  gitDiffFile{Path: "dir b/note name.txt", Status: "modified"},
		},
		{
			name:  "deleted name with trailing space",
			patch: "diff --git a/an old.txt  b/an old.txt \ndeleted file mode 100644\n--- a/an old.txt \tdate\n+++ /dev/null\tdate\n@@ -1 +0,0 @@\n-old\n",
			want:  gitDiffFile{Path: "an old.txt ", Status: "deleted", Deletions: 1},
		},
		{
			name:  "quoted destination on rename",
			patch: "diff --git a/old name.txt \"b/\\344\\270\\255.txt\"\nsimilarity index 100%\nrename from old name.txt\nrename to \"\\344\\270\\255.txt\"\n",
			want:  gitDiffFile{Path: "中.txt", Status: "modified"},
		},
		{
			name:  "copy destination with its own a prefix",
			patch: "diff --git a/dir b/source note.txt b/a/copy name.txt\nsimilarity index 100%\ncopy from dir b/source note.txt\ncopy to a/copy name.txt\n",
			want:  gitDiffFile{Path: "a/copy name.txt", Status: "modified"},
		},
		{
			name:  "binary file with spaces",
			patch: "diff --git a/a file.bin b/a file.bin\nnew file mode 100644\nBinary files /dev/null and b/a file.bin differ\n",
			want:  gitDiffFile{Path: "a file.bin", Status: "added", Binary: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseDiffFiles(tc.patch); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("parseDiffFiles() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFileEditGitPatchKeepsQuotedPathInHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	svc, root := newCodeToolsRuntime(t)
	const name = "中 note.txt"
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const patch = "diff --git \"a/\\344\\270\\255 note.txt\" \"b/\\344\\270\\255 note.txt\"\n" +
		"--- \"a/\\344\\270\\255 note.txt\"\n+++ \"b/\\344\\270\\255 note.txt\"\n" +
		"@@ -1 +1 @@\n-old\n+new\n"
	for _, dryRun := range []bool{true, false} {
		result, err := svc.Edit(context.Background(), EditRequest{Action: "patch", Patch: patch, DryRun: dryRun})
		if err != nil {
			t.Fatalf("dry_run=%t: %v", dryRun, err)
		}
		history := result[FileEditHistoryKey].(Result)
		want := gitDiffFile{Path: name, Status: "modified", Insertions: 1, Deletions: 1}
		if got := history["affected_files"].([]gitDiffFile); len(got) != 1 || got[0] != want {
			t.Fatalf("dry_run=%t affected_files = %#v, want %#v", dryRun, got, want)
		}
		wantContents := "new\n"
		if dryRun {
			wantContents = "old\n"
		}
		if contents, err := os.ReadFile(path); err != nil || string(contents) != wantContents {
			t.Fatalf("dry_run=%t file = %q, %v, want %q", dryRun, contents, err, wantContents)
		}
	}
}

func TestCountDiffStatsMultiFile(t *testing.T) {
	diff := "" +
		"diff --git a/a.txt b/a.txt\n" +
		"--- a/a.txt\n" +
		"+++ b/a.txt\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new\n" +
		"diff --git a/b.txt b/b.txt\n" +
		"--- a/b.txt\n" +
		"+++ b/b.txt\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+x\n" +
		"+y\n"
	stats := countDiffStats(diff)
	if stats.FilesChanged != 2 || stats.Insertions != 3 || stats.Deletions != 1 {
		t.Fatalf("stats = %#v", stats)
	}
}
