package observability

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTraceStoreImagePreviewMemoryAndEviction(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 1})
	if err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now().UTC()
	firstID := store.Record(TraceRecord{
		Tool:                 "view_image",
		StartedAt:            startedAt,
		ImagePreviewData:     []byte("first image"),
		ImagePreviewMimeType: "image/png",
	})
	first, mimeType, ok := store.ImagePreview(firstID)
	if !ok || mimeType != "image/png" || !bytes.Equal(first, []byte("first image")) {
		t.Fatalf("first preview = %q, %q, %v", first, mimeType, ok)
	}

	secondID := store.Record(TraceRecord{
		Tool:                 "view_image",
		StartedAt:            startedAt.Add(time.Second),
		ImagePreviewData:     []byte("second image"),
		ImagePreviewMimeType: "image/jpeg",
	})
	if _, _, ok := store.ImagePreview(firstID); ok {
		t.Fatal("evicted trace image is still available")
	}
	second, mimeType, ok := store.ImagePreview(secondID)
	if !ok || mimeType != "image/jpeg" || !bytes.Equal(second, []byte("second image")) {
		t.Fatalf("second preview = %q, %q, %v", second, mimeType, ok)
	}
}

func TestTraceStoreImagePreviewPersistsAndClears(t *testing.T) {
	root := t.TempDir()
	options := TraceOptions{
		Mode:          TraceModeDisk,
		Root:          root,
		BufferSize:    1,
		FlushInterval: time.Hour,
		MaxRecords:    10,
	}
	startedAt := time.Now().UTC()
	want := []byte("processed image bytes")

	store, err := NewTraceStore(options)
	if err != nil {
		t.Fatal(err)
	}
	id := store.Record(TraceRecord{
		Tool:                 "view_image",
		StartedAt:            startedAt,
		ImagePreviewData:     want,
		ImagePreviewMimeType: "image/png",
	})
	path := filepath.Join(root, "images", traceDay(startedAt), "1.png")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("image sidecar was not written: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewTraceStore(options)
	if err != nil {
		t.Fatal(err)
	}
	got, mimeType, ok := reopened.ImagePreview(id)
	if !ok || mimeType != "image/png" || !bytes.Equal(got, want) {
		t.Fatalf("reloaded preview = %q, %q, %v", got, mimeType, ok)
	}
	if _, err := reopened.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := reopened.ImagePreview(id); ok {
		t.Fatal("cleared trace image is still available")
	}
	if _, err := os.Stat(filepath.Join(root, "images")); !os.IsNotExist(err) {
		t.Fatalf("image sidecar directory still exists: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceStoreImagePreviewRejectsUnsupportedPayloads(t *testing.T) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory})
	if err != nil {
		t.Fatal(err)
	}
	id := store.Record(TraceRecord{
		Tool:                 "view_image",
		StartedAt:            time.Now(),
		ImagePreviewData:     []byte("not an allowed image type"),
		ImagePreviewMimeType: "text/plain",
	})
	if _, _, ok := store.ImagePreview(id); ok {
		t.Fatal("unsupported image MIME type was stored")
	}
}
