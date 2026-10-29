package observability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newFullTestStore(t *testing.T) *TraceStore {
	t.Helper()
	store, err := NewTraceStore(TraceOptions{
		Mode:          TraceModeDisk,
		Root:          t.TempDir(),
		Retention:     24 * time.Hour,
		BufferSize:    1,
		FlushInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestFullCopyCapturesBeyondPreview(t *testing.T) {
	store := newFullTestStore(t)
	stdout := strings.Repeat("b", TracePreviewBytes*2)
	store.Record(TraceRecord{
		Tool:      "exec_command",
		Success:   true,
		StartedAt: time.Now(),
		Arguments: `{"body":"` + strings.Repeat("a", TracePreviewBytes*2) + `"}`,
		Command:   &CommandTrace{Command: "echo hi", Stdout: stdout, StdoutBytes: len(stdout)},
	})

	record := store.Query(TraceQuery{})[0]
	if !record.Truncated {
		t.Fatal("oversized payload was not marked truncated")
	}
	full, ok := store.FullRecord(record.ID)
	if !ok {
		t.Fatal("full copy missing for truncated record")
	}
	if len(full.Arguments) <= TracePreviewBytes {
		t.Fatalf("full arguments = %d bytes, want beyond preview", len(full.Arguments))
	}
	if len(full.Arguments) > TraceFullBytes+len(traceTruncatedMarker) {
		t.Fatalf("full arguments = %d bytes, want <= %d", len(full.Arguments), TraceFullBytes+len(traceTruncatedMarker))
	}
	if !strings.Contains(full.Arguments, strings.Repeat("a", 1000)) {
		t.Fatal("full arguments lost body beyond preview")
	}
	if full.Command == nil {
		t.Fatal("full copy has no command payload")
	}
	if len(full.Command.Stdout) <= TraceCommandTextBytes {
		t.Fatalf("full stdout = %d bytes, want beyond preview", len(full.Command.Stdout))
	}
	if !strings.Contains(full.Command.Stdout, strings.Repeat("b", 1000)) {
		t.Fatal("full stdout lost body beyond preview")
	}
	// 预览契约不变：内存环里的正文仍被钳在 8 KiB。
	if len(record.Arguments) > TracePreviewBytes+len(traceTruncatedMarker) {
		t.Fatalf("preview arguments = %d bytes", len(record.Arguments))
	}
	if full.FullTruncated {
		t.Fatal("payload within full limit must not set full_truncated")
	}
}

func TestFullCopyTruncatesAtFullLimit(t *testing.T) {
	store := newFullTestStore(t)
	store.Record(TraceRecord{
		Tool:      "read_file",
		Success:   true,
		StartedAt: time.Now(),
		Arguments: `{"body":"` + strings.Repeat("a", TraceFullBytes*2) + `"}`,
	})

	record := store.Query(TraceQuery{})[0]
	full, ok := store.FullRecord(record.ID)
	if !ok {
		t.Fatal("full copy missing")
	}
	if !full.FullTruncated {
		t.Fatal("oversized payload was not marked full_truncated")
	}
	if len(full.Arguments) > TraceFullBytes+len(traceTruncatedMarker) {
		t.Fatalf("full arguments = %d bytes", len(full.Arguments))
	}
}

func TestFullCopyRedactsBeyondPreview(t *testing.T) {
	store := newFullTestStore(t)
	// 凭据落在预览窗口之外：全文副本同样不允许带出未脱敏字节。
	store.Record(TraceRecord{
		Tool:      "read_file",
		Success:   true,
		StartedAt: time.Now(),
		Arguments: `{"body":"` + strings.Repeat("a", TracePreviewBytes*2) + `","token":"supersecret"}`,
	})

	record := store.Query(TraceQuery{})[0]
	full, ok := store.FullRecord(record.ID)
	if !ok {
		t.Fatal("full copy missing")
	}
	if strings.Contains(full.Arguments, "supersecret") {
		t.Fatalf("full arguments leaked secret: %q", full.Arguments)
	}
}

func TestFullCopySkippedForSmallPayload(t *testing.T) {
	store := newFullTestStore(t)
	store.Record(TraceRecord{
		Tool:      "read_file",
		Success:   true,
		StartedAt: time.Now(),
		Arguments: `{"path":"a.go"}`,
		Result:    `{"ok":true}`,
	})

	record := store.Query(TraceQuery{})[0]
	if _, ok := store.FullRecord(record.ID); ok {
		t.Fatal("small payload must not write a full sidecar copy")
	}
}

func TestClearRemovesFullCopies(t *testing.T) {
	store := newFullTestStore(t)
	store.Record(TraceRecord{
		Tool:      "read_file",
		Success:   true,
		StartedAt: time.Now(),
		Arguments: `{"body":"` + strings.Repeat("a", TracePreviewBytes*2) + `"}`,
	})
	record := store.Query(TraceQuery{})[0]
	if _, ok := store.FullRecord(record.ID); !ok {
		t.Fatal("full copy missing before clear")
	}

	if _, err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.FullRecord(record.ID); ok {
		t.Fatal("full copy survived Clear")
	}
}

func TestCleanupExpiredRemovesFullFiles(t *testing.T) {
	store := newFullTestStore(t)
	expired := filepath.Join(store.fullDir(), "2020-01-01.jsonl")
	if err := os.MkdirAll(store.fullDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expired, []byte(`{"id":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store.cleanupExpired()
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatalf("expired full file still exists: %v", err)
	}
}

func TestDiskBytesIncludesFullCopies(t *testing.T) {
	store := newFullTestStore(t)
	if err := os.MkdirAll(store.fullDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(store.fullDir(), "2020-01-01.jsonl")
	if err := os.WriteFile(full, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := store.DiskBytes(); got < 4096 {
		t.Fatalf("DiskBytes() = %d, want >= 4096", got)
	}
}
