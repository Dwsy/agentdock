//go:build darwin || linux

package file

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestListDirRollsBackToGoKernelWhenFDUnavailable 覆盖回滚路径：fd 不在 PATH，
// 或发行版自带的旧 fd 不识别参数（exit 2）时，list_dir 必须自动降级到 go_fallback。
func TestListDirRollsBackToGoKernelWhenFDUnavailable(t *testing.T) {
	service, root := newFileTestService(t)
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertFallback := func(t *testing.T) {
		t.Helper()
		result, err := service.listDirTest(context.Background(), ListRequest{Path: ".", EntryType: "file"})
		if err != nil {
			t.Fatalf("list_dir error = %v", err)
		}
		if result["engine"] != "go_fallback" {
			t.Fatalf("engine = %#v, want go_fallback", result["engine"])
		}
		entries := result["entries"].([]map[string]any)
		if len(entries) != 1 || entries[0]["path"] != "keep.txt" {
			t.Fatalf("entries = %#v", entries)
		}
	}

	t.Run("fd missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		assertFallback(t)
	})

	t.Run("fd rejects arguments", func(t *testing.T) {
		shim := t.TempDir()
		if err := os.WriteFile(filepath.Join(shim, "fd"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
		assertFallback(t)
	})
}

// TestListDirKernelsSkipUnreadableDescendant 确认 fd 内核与 Go 回滚内核对不可读目录
// 的 partial/skipped_paths 语义一致：fd 会静默跳过权限不足的目录，fd 内核必须显式探测。
func TestListDirKernelsSkipUnreadableDescendant(t *testing.T) {
	service, root := newFileTestService(t)
	visibleDir := filepath.Join(root, "visible")
	if err := os.MkdirAll(visibleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(visibleDir, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	blockedDir := filepath.Join(root, "blocked")
	if err := os.MkdirAll(blockedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blockedDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blockedDir, 0o755) })

	request := ListRequest{Path: ".", MaxDepth: intPtrForTest(2), Patterns: []string{"**/*.txt"}, EntryType: "file"}
	assertResult := func(t *testing.T, result Result, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("list_dir returned a descendant permission error: %v", err)
		}
		entries := result["entries"].([]map[string]any)
		if len(entries) != 1 || entries[0]["path"] != "visible/ok.txt" {
			t.Fatalf("entries = %#v", entries)
		}
		if result["partial"] != true {
			t.Fatalf("partial = %#v", result["partial"])
		}
		skipped := result["skipped_paths"].([]string)
		if len(skipped) != 1 || skipped[0] != "blocked" {
			t.Fatalf("skipped_paths = %#v", skipped)
		}
	}

	t.Run("fd", func(t *testing.T) {
		if _, err := exec.LookPath("fd"); err != nil {
			t.Skip("fd is not installed")
		}
		result, err := service.listDirTest(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result["engine"] != "fd" {
			t.Fatalf("engine = %#v, want fd", result["engine"])
		}
		assertResult(t, result, nil)
	})

	t.Run("go_fallback", func(t *testing.T) {
		opts, err := parseListDirOptions(request)
		if err != nil {
			t.Fatal(err)
		}
		path, err := service.ws.ResolveExisting(".")
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.listDirGo(context.Background(), path, opts)
		if err != nil {
			t.Fatal(err)
		}
		if result["engine"] != "go_fallback" {
			t.Fatalf("engine = %#v, want go_fallback", result["engine"])
		}
		assertResult(t, result, nil)
	})
}
