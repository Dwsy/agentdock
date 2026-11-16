package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

func TestRuntimeTraceImageReturnsPrivatePreviewPayload(t *testing.T) {
	runtime := newTraceTestRuntime(t, config.TraceModeMemory)
	want := []byte("processed image bytes")
	id := runtime.traces.Record(observability.TraceRecord{
		Tool:                 "view_image",
		StartedAt:            time.Now().UTC(),
		ImagePreviewData:     want,
		ImagePreviewMimeType: "image/png",
	})

	result, err := runtime.RuntimeTraceImage(id)
	if err != nil {
		t.Fatal(err)
	}
	if result["mime_type"] != "image/png" {
		t.Fatalf("mime_type = %#v", result["mime_type"])
	}
	data, ok := result["data"].([]byte)
	if !ok || string(data) != string(want) {
		t.Fatalf("image data = %#v", result["data"])
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) || !bytes.Contains(encoded, []byte(base64.StdEncoding.EncodeToString(want))) {
		t.Fatalf("image response does not encode base64 data: %s", encoded)
	}
}

func TestRuntimeTraceImageRejectsMissingPreview(t *testing.T) {
	runtime := newTraceTestRuntime(t, config.TraceModeMemory)
	_, err := runtime.RuntimeTraceImage(42)
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "TRACE_IMAGE_NOT_FOUND" {
		t.Fatalf("error = %#v", err)
	}
}
