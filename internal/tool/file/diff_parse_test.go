package file

import "testing"

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
