package observability

import (
	"net/http"
	"strings"
	"testing"
)

func TestCaptureStoreDisabledIsNil(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name    string
		enabled bool
		max     int
	}{
		{name: "disabled", enabled: false, max: 100},
		{name: "zero max", enabled: true, max: 0},
		{name: "negative max", enabled: true, max: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewCaptureStore(tc.enabled, tc.max)
			if store != nil {
				t.Fatalf("NewCaptureStore(%v, %d) = %v, want nil", tc.enabled, tc.max, store)
			}
			if store.Enabled() {
				t.Fatal("nil store must report disabled")
			}
			store.Record(CaptureRecord{Method: http.MethodGet})
			if got := len(store.Query(CaptureQuery{})); got != 0 {
				t.Fatalf("nil store query = %d records, want 0", got)
			}
		})
	}
}

func TestCaptureStoreKeepsNewestWithinMax(t *testing.T) {
	store := NewCaptureStore(true, 3)
	for i := 1; i <= 5; i++ {
		store.Record(CaptureRecord{Method: http.MethodGet, Path: "/p", Query: strings.Repeat("x", i)})
	}
	records := store.Query(CaptureQuery{})
	if len(records) != 3 {
		t.Fatalf("query returned %d records, want 3", len(records))
	}
	// 环必须淘汰最旧记录，并且按时间倒序返回。
	wantQueries := []string{"xxxxx", "xxxx", "xxx"}
	for i, record := range records {
		if record.Query != wantQueries[i] {
			t.Fatalf("records[%d].Query = %q, want %q", i, record.Query, wantQueries[i])
		}
	}
	if status := store.Status(); status.Count != 3 || status.MaxRecords != 3 || !status.Enabled {
		t.Fatalf("status = %+v, want enabled with 3/3", status)
	}
}

func TestCaptureStoreSummaryDropsPayload(t *testing.T) {
	store := NewCaptureStore(true, 10)
	store.Record(CaptureRecord{
		Method: http.MethodPost,
		Path:   "/mcp",
		Request: CaptureMessage{
			Headers: []CaptureHeader{{Name: "Authorization", Value: "Bearer secret"}},
			Body:    `{"jsonrpc":"2.0"}`,
			Bytes:   17,
		},
		Response: CaptureMessage{Body: "ok", Bytes: 2},
	})
	full := store.Query(CaptureQuery{})
	if len(full) != 1 || full[0].Request.Body == "" || len(full[0].Request.Headers) != 1 {
		t.Fatalf("full query = %+v, want request payload intact", full)
	}
	summary := store.Query(CaptureQuery{Summary: true})
	if len(summary) != 1 {
		t.Fatalf("summary query returned %d records, want 1", len(summary))
	}
	record := summary[0]
	if record.Request.Body != "" || record.Response.Body != "" {
		t.Fatalf("summary must drop bodies, got request=%q response=%q", record.Request.Body, record.Response.Body)
	}
	if len(record.Request.Headers) != 0 || len(record.Response.Headers) != 0 {
		t.Fatalf("summary must drop headers, got %+v", record.Request.Headers)
	}
	// 裁剪必须只作用于投影：详情页仍要拿到原文。
	detail, ok := store.RecordByID(full[0].ID)
	if !ok || detail.Request.Body != `{"jsonrpc":"2.0"}` {
		t.Fatalf("detail = %+v, want original body preserved", detail)
	}
}

func TestCaptureStoreFilters(t *testing.T) {
	store := NewCaptureStore(true, 10)
	store.Record(CaptureRecord{Channel: CaptureChannelHTTP, Method: http.MethodGet, Path: "/healthz", Status: 200, Outcome: CaptureOutcomeOK})
	store.Record(CaptureRecord{Channel: CaptureChannelNexus, Method: "tool.call", Path: "search_files", Outcome: CaptureOutcomeOK})
	store.Record(CaptureRecord{Channel: CaptureChannelHTTP, Method: http.MethodPost, Path: "/mcp", Status: 500, Outcome: CaptureOutcomeError})

	for _, tc := range []struct {
		name  string
		query CaptureQuery
		want  int
	}{
		{name: "channel", query: CaptureQuery{Channel: "nexus"}, want: 1},
		{name: "method", query: CaptureQuery{Method: "post"}, want: 1},
		{name: "status code", query: CaptureQuery{Status: "500"}, want: 1},
		{name: "outcome error", query: CaptureQuery{Status: "error"}, want: 1},
		{name: "outcome ok", query: CaptureQuery{Status: "ok"}, want: 2},
		{name: "needle path", query: CaptureQuery{Query: "health"}, want: 1},
		{name: "needle nexus tool", query: CaptureQuery{Query: "search_files"}, want: 1},
		{name: "no match", query: CaptureQuery{Query: "nothing"}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(store.Query(tc.query)); got != tc.want {
				t.Fatalf("Query(%+v) = %d records, want %d", tc.query, got, tc.want)
			}
		})
	}
}

func TestCaptureStoreRecordByIDAndClear(t *testing.T) {
	store := NewCaptureStore(true, 2)
	store.Record(CaptureRecord{Path: "/first"})
	records := store.Query(CaptureQuery{})
	if len(records) != 1 {
		t.Fatalf("query returned %d records, want 1", len(records))
	}
	first := records[0]
	if _, ok := store.RecordByID(first.ID); !ok {
		t.Fatalf("RecordByID(%d) not found", first.ID)
	}
	if _, ok := store.RecordByID(first.ID + 999); ok {
		t.Fatal("RecordByID must miss unknown ids")
	}
	if cleared := store.Clear(); cleared != 1 {
		t.Fatalf("Clear() = %d, want 1", cleared)
	}
	if _, ok := store.RecordByID(first.ID); ok {
		t.Fatal("cleared record must not be retrievable")
	}
}

func TestCaptureHeadersBoundsTotalBytes(t *testing.T) {
	header := http.Header{}
	header.Add("X-Trace", strings.Repeat("a", 128))
	header.Add("Authorization", "Bearer token")

	headers, truncated := CaptureHeaders(header, 40)
	if !truncated {
		t.Fatal("CaptureHeaders must truncate when the limit is exceeded")
	}
	total := 0
	for _, item := range headers {
		total += len(item.Name) + len(item.Value)
	}
	if total > 40 {
		t.Fatalf("captured %d header bytes, want <= 40", total)
	}
	if len(headers) == 0 {
		t.Fatal("CaptureHeaders must keep the headers that fit")
	}

	// 同名 header 必须逐条保留，不能被 map 合并掉。
	repeated := http.Header{}
	repeated.Add("X-Tag", "a")
	repeated.Add("X-Tag", "b")
	got, truncated := CaptureHeaders(repeated, 1024)
	if truncated {
		t.Fatal("CaptureHeaders truncated unexpectedly")
	}
	if len(got) != 2 || got[0].Value != "a" || got[1].Value != "b" {
		t.Fatalf("CaptureHeaders = %+v, want both X-Tag values in order", got)
	}
}
