package file

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchTextRGAcceptsFileAndLeadingHyphenQuery(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
	service, root := newFileTestService(t)
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("needle --needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path, query string
	}{
		{"single file", "sample.txt", "needle"},
		{"leading hyphen", ".", "--needle"},
		{"known option as text", ".", "--files"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := service.SearchText(context.Background(), SearchRequest{Path: test.path, Query: test.query})
			if err != nil {
				t.Fatal(err)
			}
			matches := result["matches"].([]map[string]any)
			want := 1
			if test.query == "--files" {
				want = 0
			}
			if result["engine"] != "rg" || len(matches) != want {
				t.Fatalf("result = %#v, want rg with %d matches", result, want)
			}
			if want > 0 && matches[0]["path"] != "sample.txt" {
				t.Fatalf("match path = %#v, want sample.txt", matches[0]["path"])
			}
		})
	}
}

func TestSearchTextTruncatesEarlyForRGAndGoFallback(t *testing.T) {
	service, root := newFileTestService(t)
	var content strings.Builder
	for i := 0; i < 60; i++ {
		content.WriteString(fmt.Sprintf("line %02d needle-token-value\n", i))
	}
	for f := 0; f < 5; f++ {
		name := filepath.Join(root, fmt.Sprintf("doc_%d.txt", f))
		if err := os.WriteFile(name, []byte(content.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	assertTruncatedSearch := func(t *testing.T, wantEngine string) {
		t.Helper()
		maxRes := 5
		result, err := service.SearchText(context.Background(), SearchRequest{
			Path:          ".",
			Query:         "needle-token-value",
			CaseSensitive: true,
			MaxResults:    &maxRes,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result["engine"] != wantEngine {
			t.Fatalf("engine = %v, want %q", result["engine"], wantEngine)
		}
		matches := result["matches"].([]map[string]any)
		if len(matches) != 5 {
			t.Fatalf("len(matches) = %d, want 5", len(matches))
		}
		if truncated, _ := result["truncated"].(bool); !truncated {
			t.Fatalf("expected truncated = true")
		}
	}

	if _, err := exec.LookPath("rg"); err == nil {
		t.Run("rg", func(t *testing.T) {
			assertTruncatedSearch(t, "rg")
		})
	}
	t.Run("go_fallback", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		assertTruncatedSearch(t, "go_fallback")
	})
}
