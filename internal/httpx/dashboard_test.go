package httpx

import (
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
