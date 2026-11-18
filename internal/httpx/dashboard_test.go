package httpx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeDashboardJSON(t *testing.T) {
	input := `{"ok":true,"records":[{"request":{"headers":[{"name":"Authorization","value":"Bearer abcdefghijklmnopqrst"},{"name":"Accept","value":"*/*"}]}},{"password":"plain-secret-value-alpha","detail":"Bearer zzzzzzzzzzzz tail"}]}`
	got := sanitizeDashboardJSON([]byte(input))

	// 结构化字段必须脱敏。
	if strings.Contains(string(got), "abcdefghijklmnopqrst") {
		t.Fatalf("Bearer token not redacted: %s", got)
	}
	if strings.Contains(string(got), "plain-secret-value-alpha") {
		t.Fatalf("password field not redacted: %s", got)
	}
	if strings.Contains(string(got), "zzzzzzzzzzzz") {
		t.Fatalf("trailing Bearer not redacted: %s", got)
	}
	// 非敏感字段必须保留。
	if !strings.Contains(string(got), "Accept") || !strings.Contains(string(got), "*/*") {
		t.Fatalf("non-sensitive fields lost: %s", got)
	}
	// 结果仍是合法 JSON。
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	// Authorization 头的值应为 [REDACTED]，name 保留。
	records := m["records"].([]any)
	headers := records[0].(map[string]any)["request"].(map[string]any)["headers"].([]any)
	auth := headers[0].(map[string]any)
	if auth["name"] != "Authorization" || auth["value"] != "[REDACTED]" {
		t.Fatalf("auth header shaping wrong: %#v", auth)
	}
}

func TestSanitizeDashboardJSONNonJSON(t *testing.T) {
	got := sanitizeDashboardJSON([]byte("oops Bearer abcdefghijklmnop text"))
	if strings.Contains(string(got), "abcdefghijklmnop") {
		t.Fatalf("non-JSON fallback failed: %s", got)
	}
}

func TestSanitizeDashboardJSONEdgeCasesAndFastPath(t *testing.T) {
	cleanAnalytics := []byte(`{"total_calls":128,"total_input_tokens":45000,"daily_tokens":[{"date":"2026-11-18","input":1200}],"status":"ok"}`)
	gotClean := sanitizeDashboardJSON(cleanAnalytics)
	if !bytes.Equal(gotClean, cleanAnalytics) {
		t.Fatalf("clean payload altered: %s", gotClean)
	}

	cases := []struct {
		name     string
		input    string
		leaked   string
		retained string
	}{
		{
			name:     "unicode escaped key",
			input:    `{"\u0074oken":"secret-unicode-value","ok":true}`,
			leaked:   "secret-unicode-value",
			retained: `"ok":true`,
		},
		{
			name:     "case variations",
			input:    `{"X-API-Key":"k1","Proxy-Authorization":"k2","Set-Cookie":"c1","detail":"bearer xyz12345678","ok":true}`,
			leaked:   "xyz12345678",
			retained: `[REDACTED]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(sanitizeDashboardJSON([]byte(tc.input)))
			if strings.Contains(got, tc.leaked) {
				t.Fatalf("secret leaked in %q: %s", tc.name, got)
			}
			if !strings.Contains(got, tc.retained) {
				t.Fatalf("expected %q in %s", tc.retained, got)
			}
		})
	}

	pngHeader := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRbinarypayload")
	if gotPNG := sanitizeDashboardJSON(pngHeader); !bytes.Equal(gotPNG, pngHeader) {
		t.Fatalf("binary PNG payload altered")
	}
}

func BenchmarkSanitizeDashboardJSONCleanAnalytics(b *testing.B) {
	payload := []byte(`{"total_calls":500,"total_input_tokens":92000,"total_output_tokens":34000,"daily_tokens":[{"date":"2026-11-18","input":12000,"output":4500}],"recent_calls":[{"id":1,"tool":"read_file","success":true}]}`)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(sanitizeDashboardJSON(payload)) == 0 {
			b.Fatal("empty output")
		}
	}
}

func BenchmarkSanitizeDashboardJSONWithSecrets(b *testing.B) {
	payload := []byte(`{"ok":true,"records":[{"headers":[{"name":"Authorization","value":"Bearer abcdefghijklmnopqrst"},{"name":"Accept","value":"*/*"}],"password":"hidden-password"}]}`)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(sanitizeDashboardJSON(payload)) == 0 {
			b.Fatal("empty output")
		}
	}
}
