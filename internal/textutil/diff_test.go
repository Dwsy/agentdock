package textutil

import "testing"

func TestDiffSectionsSplitsGitStyleDiff(t *testing.T) {
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
	sections := DiffSections(diff)
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2: %#v", len(sections), sections)
	}
	if sections[0].Path != "a.txt" || sections[0].Insertions != 1 || sections[0].Deletions != 1 {
		t.Fatalf("a.txt = %#v", sections[0])
	}
	if sections[1].Path != "b.txt" || sections[1].Insertions != 2 || sections[1].Deletions != 0 {
		t.Fatalf("b.txt = %#v", sections[1])
	}
}

// envelope patch 的 stagedDiffPreview 只产 ---/+++ 头，没有 diff --git 行。
func TestDiffSectionsParsesHeaderPairOnlyDiff(t *testing.T) {
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
		"+created\n"
	sections := DiffSections(diff)
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2: %#v", len(sections), sections)
	}
	if sections[0].Path != "src/x.go" || sections[0].Insertions != 1 || sections[0].Deletions != 1 {
		t.Fatalf("x.go = %#v", sections[0])
	}
	if sections[1].Path != "src/y.go" || sections[1].Insertions != 1 {
		t.Fatalf("y.go = %#v", sections[1])
	}
}

// 删除 "-- x" 渲染为 "--- x"：不能被误当文件头漏计。
func TestDiffSectionsCountsHeaderLikeContentLines(t *testing.T) {
	diff := "" +
		"--- a/a.txt\n" +
		"+++ b/a.txt\n" +
		"@@ -1,3 +1,2 @@\n" +
		" keep\n" +
		"--- dashes\n" +
		"+++ pluses\n"
	sections := DiffSections(diff)
	if len(sections) != 1 || sections[0].Deletions != 1 || sections[0].Insertions != 1 {
		t.Fatalf("sections = %#v", sections)
	}
}

func TestDiffHeaderPath(t *testing.T) {
	for field, want := range map[string]string{
		"a/x.go":                       "x.go",
		"b/x.go":                       "x.go",
		"/dev/null":                    "",
		"":                             "",
		"b/dir with space":             "dir with space",
		"a/ts.go\t2026-01-01 10:00:00": "ts.go",
	} {
		if got := DiffHeaderPath(field); got != want {
			t.Fatalf("DiffHeaderPath(%q) = %q, want %q", field, got, want)
		}
	}
}

func TestDiffSectionsEmptyInput(t *testing.T) {
	if sections := DiffSections(""); sections != nil {
		t.Fatalf("empty input = %#v", sections)
	}
}
