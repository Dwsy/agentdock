package app

import (
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
)

func TestRuntimeTraceDetailFullCopy(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeDisk)
	body := strings.Repeat("x", observability.TracePreviewBytes*2)
	rt.traces.Record(observability.TraceRecord{
		Tool:      "read_file",
		Success:   true,
		StartedAt: time.Now(),
		Arguments: `{"body":"` + body + `"}`,
	})
	rt.traces.Record(observability.TraceRecord{
		Tool:      "read_file",
		Success:   true,
		StartedAt: time.Now(),
		Arguments: `{"path":"a.go"}`,
	})
	records := rt.traces.Query(observability.TraceQuery{})
	if len(records) != 2 {
		t.Fatalf("records = %d", len(records))
	}
	// Query 返回新到旧：records[0] 是小 payload 记录。
	smallID, truncatedID := records[0].ID, records[1].ID

	detail, err := rt.RuntimeTraceDetail(truncatedID, true)
	if err != nil {
		t.Fatal(err)
	}
	if detail["full_available"] != true {
		t.Fatalf("full_available = %#v", detail["full_available"])
	}
	full, ok := detail["full"].(observability.TraceFullRecord)
	if !ok {
		t.Fatalf("full = %#v", detail["full"])
	}
	if len(full.Arguments) <= observability.TracePreviewBytes {
		t.Fatalf("full arguments = %d bytes, want beyond preview", len(full.Arguments))
	}

	smallDetail, err := rt.RuntimeTraceDetail(smallID, true)
	if err != nil {
		t.Fatal(err)
	}
	if smallDetail["full_available"] != false || smallDetail["full"] != nil {
		t.Fatalf("small record full = %#v", smallDetail)
	}

	previewDetail, err := rt.RuntimeTraceDetail(truncatedID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := previewDetail["full"]; has {
		t.Fatal("full=false must not attach full payload")
	}
}
