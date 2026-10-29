package publicartifacts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRemoveDeletesArtifactDirectory(t *testing.T) {
	root := t.TempDir()
	store := New(filepath.Join(root, "home"), "https://agent.example", 8765)
	result, err := store.PublishBytes(PublishBytesRequest{
		Filename: "report.txt", Data: []byte("payload"), MimeType: "text/plain",
		RetentionSeconds: 3600, Now: time.Now().UTC(), BaseURL: "https://agent.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.Root, result.ArtifactID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("artifact dir missing after publish: %v", err)
	}

	if err := store.Remove(result.ArtifactID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("artifact dir should be removed: %v", err)
	}
	items, err := store.List(time.Now().UTC(), "https://agent.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("list after remove = %#v", items)
	}

	if err := store.Remove(result.ArtifactID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove error = %v, want ErrNotFound", err)
	}
}

func TestRemoveRejectsInvalidAndTraversingIDs(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	store := New(home, "https://agent.example", 8765)
	sentinel := filepath.Join(root, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"",
		".",
		"..",
		"../sentinel.txt",
		filepath.FromSlash("../../sentinel.txt"),
		"/etc/passwd",
		"0123456789abcdef0123456789abcde",  // 31 chars
		"0123456789abcdef0123456789abcdeF", // uppercase hex
		"0123456789abcdef0123456789abcdeg", // non-hex tail
		"0123456789abcdef0123456789abcdef/../../sentinel.txt",
	} {
		t.Run("id="+id, func(t *testing.T) {
			if err := store.Remove(id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Remove(%q) error = %v, want ErrNotFound", id, err)
			}
		})
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel file must survive invalid removals: %v", err)
	}
}

func TestRemoveRejectsUnknownArtifact(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "home"), "https://agent.example", 8765)
	if err := store.Remove("0123456789abcdef0123456789abcdef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestExtendRewritesMetadataAndResignsURL(t *testing.T) {
	root := t.TempDir()
	store := New(filepath.Join(root, "home"), "https://agent.example", 8765)
	created := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	result, err := store.PublishBytes(PublishBytesRequest{
		Filename: "report.txt", Data: []byte("payload"), MimeType: "text/plain",
		RetentionSeconds: 3 * 3600, Now: created, BaseURL: "https://agent.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	oldURL := result.URL
	now := created.Add(2 * time.Hour)
	extended, err := store.Extend(result.ArtifactID, 3*time.Hour, now, "https://agent.example")
	if err != nil {
		t.Fatal(err)
	}
	want := now.Add(3 * time.Hour)
	if !extended.ExpiresAt.Equal(want) {
		t.Fatalf("expires = %v, want %v", extended.ExpiresAt, want)
	}
	if extended.URL == oldURL || !strings.Contains(extended.URL, "expires="+strconv.FormatInt(want.Unix(), 10)) {
		t.Fatalf("resigned url = %q, old = %q", extended.URL, oldURL)
	}

	// metadata.json 必须落盘重写，不能只改内存返回值。
	raw, err := os.ReadFile(filepath.Join(store.Root, result.ArtifactID, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk Metadata
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if !onDisk.ExpiresAt.Equal(want) {
		t.Fatalf("metadata.json expires = %v, want %v", onDisk.ExpiresAt, want)
	}
	if onDisk.CreatedAt.Equal(extended.ExpiresAt) || !onDisk.CreatedAt.Equal(created) {
		t.Fatalf("created_at must not change: %#v", onDisk)
	}

	// 新链接可下载；旧链接因为签名覆盖 expires 立即失效。
	body, status := download(t, store, extended.URL)
	if status != 200 || string(body) != "payload" {
		t.Fatalf("extended url download = %d %q", status, body)
	}
	if _, status := download(t, store, oldURL); status != 404 {
		t.Fatalf("old url status = %d, want 404", status)
	}
}

func TestExtendClampsToMaxRetention(t *testing.T) {
	root := t.TempDir()
	store := New(filepath.Join(root, "home"), "https://agent.example", 8765)
	created := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	result, err := store.PublishBytes(PublishBytesRequest{
		Filename: "report.txt", Data: []byte("payload"), MimeType: "text/plain",
		RetentionSeconds: 3600, Now: created, BaseURL: "https://agent.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := created.Add(24 * time.Hour)

	// 10 天超过 created_at+MaxRetention，必须钳到 MaxRetention。
	extended, err := store.Extend(result.ArtifactID, 10*24*time.Hour, now, "https://agent.example")
	if err != nil {
		t.Fatal(err)
	}
	clamped := created.Add(MaxRetention)
	if !extended.ExpiresAt.Equal(clamped) {
		t.Fatalf("clamped expires = %v, want %v", extended.ExpiresAt, clamped)
	}
	if !strings.Contains(extended.URL, "expires="+strconv.FormatInt(clamped.Unix(), 10)) {
		t.Fatalf("clamped url = %q", extended.URL)
	}

	// 未触顶时按 now+duration。
	extended, err = store.Extend(result.ArtifactID, 2*time.Hour, now, "https://agent.example")
	if err != nil {
		t.Fatal(err)
	}
	if !extended.ExpiresAt.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("expires = %v, want %v", extended.ExpiresAt, now.Add(2*time.Hour))
	}
}

func TestExtendRejectsUnknownAndInvalidIDs(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "home"), "https://agent.example", 8765)
	for _, id := range []string{"0123456789abcdef0123456789abcdef", "../escape", "nope"} {
		if _, err := store.Extend(id, time.Hour, time.Now().UTC(), "https://agent.example"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Extend(%q) error = %v, want ErrNotFound", id, err)
		}
	}
}

func TestExtendRejectsNonPositiveDuration(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "home"), "https://agent.example", 8765)
	result, err := store.PublishBytes(PublishBytesRequest{
		Filename: "report.txt", Data: []byte("payload"), MimeType: "text/plain",
		RetentionSeconds: 3600, Now: time.Now().UTC(), BaseURL: "https://agent.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Extend(result.ArtifactID, 0, time.Now().UTC(), "https://agent.example"); err == nil {
		t.Fatal("expected non-positive duration error")
	}
	if _, err := store.Extend(result.ArtifactID, -time.Hour, time.Now().UTC(), "https://agent.example"); err == nil {
		t.Fatal("expected negative duration error")
	}
}
