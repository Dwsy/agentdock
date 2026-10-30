package file

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestListDirFDEngineListsFilteredEntries(t *testing.T) {
	if _, err := exec.LookPath("fd"); err != nil {
		t.Skip("fd is not installed")
	}
	service, root := newFileTestService(t)
	for path, content := range map[string]string{
		"visible.txt":          "visible",
		".hidden.txt":          "hidden",
		"nested/child.txt":     "child",
		"nested/deep/file.txt": "deep",
		"node_modules/skip.js": "skip",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	result, err := service.listDirTest(context.Background(), map[string]any{"path": ".", "max_depth": 2, "max_entries": 100})
	if err != nil {
		t.Fatalf("list_dir error = %v", err)
	}
	if result["engine"] != "fd" {
		t.Fatalf("engine = %#v, want fd", result["engine"])
	}
	paths := map[string]bool{}
	for _, entry := range result["entries"].([]map[string]any) {
		paths[entry["path"].(string)] = true
	}
	for _, expected := range []string{"visible.txt", "nested", "nested/child.txt", "nested/deep"} {
		if !paths[expected] {
			t.Fatalf("missing path %q in %#v", expected, paths)
		}
	}
	if paths[".hidden.txt"] || paths["nested/deep/file.txt"] || paths["node_modules"] {
		t.Fatalf("hidden, over-depth, or skipped path leaked: %#v", paths)
	}
}

func TestListDirGoReportsFallbackEngine(t *testing.T) {
	service, root := newFileTestService(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "d.txt"), []byte("d"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := ListRequest{Path: ".", EntryType: "file", Patterns: []string{"*.txt"}}
	depth := 2
	request.MaxDepth = &depth
	maxEntries := 3
	request.MaxEntries = &maxEntries

	opts, err := parseListDirOptions(request)
	if err != nil {
		t.Fatal(err)
	}
	path, err := service.ws.ResolveExisting(".")
	if err != nil {
		t.Fatal(err)
	}

	// *.txt 单段语义不该匹配 sub/d.txt；这是回滚内核也必须保持的过滤契约。
	exact, err := service.listDirGo(context.Background(), path, opts)
	if err != nil {
		t.Fatalf("listDirGo error = %v", err)
	}
	if exact["engine"] != "go_fallback" {
		t.Fatalf("engine = %#v, want go_fallback", exact["engine"])
	}
	entries := exact["entries"].([]map[string]any)
	if exact["truncated"] != false || len(entries) != 3 || entries[0]["path"] != "a.txt" || entries[2]["path"] != "c.txt" {
		t.Fatalf("exact result = %#v, want a.txt..c.txt untruncated", exact)
	}

	*request.MaxEntries = 2
	overflowOpts, err := parseListDirOptions(request)
	if err != nil {
		t.Fatal(err)
	}
	overflow, err := service.listDirGo(context.Background(), path, overflowOpts)
	if err != nil {
		t.Fatalf("listDirGo error = %v", err)
	}
	if overflow["truncated"] != true || len(overflow["entries"].([]map[string]any)) != 2 {
		t.Fatalf("overflow result = %#v, want two entries and truncated=true", overflow)
	}
}
