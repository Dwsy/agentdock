package core

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	input := "password=abc github_pat_123456 https://user:token@github.com/owner/repo.git"
	got := RedactSecrets(input, nil)
	for _, leaked := range []string{"abc", "github_pat_123456", "token@github.com"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q: %s", leaked, got)
		}
	}
}

func TestRedactSecretsVariantsAndExtraPatterns(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		extra  []string
		leaked string
	}{
		{"clean text unchanged", `{"ok":true,"path":"main.go"}`, nil, ""},
		{"ghp token", "export TOKEN=ghp_1234567890abcdef", nil, "ghp_1234567890abcdef"},
		{"ghu token", "ghu_abcdef123456", nil, "ghu_abcdef123456"},
		{"case-insensitive secret param", "https://example.com?Secret=mysecretvalue&x=1", nil, "mysecretvalue"},
		{"extra cached pattern", "custom_key=alpha123", []string{`custom_key=[a-z0-9]+`}, "alpha123"},
		{"invalid extra pattern ignored", "clean line", []string{`[invalid(`}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactSecrets(tc.input, tc.extra)
			if tc.leaked == "" && got != tc.input {
				t.Fatalf("expected unchanged %q, got %q", tc.input, got)
			}
			if tc.leaked != "" && strings.Contains(got, tc.leaked) {
				t.Fatalf("secret %q leaked: %s", tc.leaked, got)
			}
		})
	}
}

func BenchmarkRedactSecretsClean(b *testing.B) {
	sample := `{"ok":true,"path":"internal/app/runtime.go","content":"` + strings.Repeat("ordinary line of code; ", 200) + `"}`
	b.SetBytes(int64(len(sample)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if RedactSecrets(sample, nil) == "" {
			b.Fatal("empty")
		}
	}
}

func BenchmarkRedactSecretsWithExtraPatterns(b *testing.B) {
	sample := "normal line without match custom_prefix_none"
	patterns := []string{`sk-[A-Za-z0-9]{16,}`, `ak_[A-Za-z0-9]{16,}`}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if RedactSecrets(sample, patterns) == "" {
			b.Fatal("empty")
		}
	}
}
