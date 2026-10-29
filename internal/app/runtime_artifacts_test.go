package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

func TestRuntimeArtifactsListsPublishedFiles(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	source := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(source, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.artifacts.Publish(publicartifacts.PublishRequest{Path: source}); err != nil {
		t.Fatal(err)
	}

	result, err := rt.RuntimeArtifacts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := result["artifacts"].([]publicartifacts.PublishResult)
	if len(items) != 1 || items[0].Filename != "hello.txt" {
		t.Fatalf("artifacts = %#v", items)
	}
	if result["configured"] != false || result["public_base_url"] != "" {
		t.Fatalf("base url should be empty without public server: %#v", result)
	}
	if items[0].URL != "" {
		t.Fatalf("url without base = %q", items[0].URL)
	}
}

func TestRuntimeArtifactsBuildsSignedURL(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
		OAuthServerURL:      "https://example.test",
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	source := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(source, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.artifacts.Publish(publicartifacts.PublishRequest{Path: source}); err != nil {
		t.Fatal(err)
	}

	result, err := rt.RuntimeArtifacts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result["configured"] != true {
		t.Fatalf("configured = %#v", result["configured"])
	}
	items := result["artifacts"].([]publicartifacts.PublishResult)
	if len(items) != 1 {
		t.Fatalf("artifacts = %#v", items)
	}
	link := items[0].URL
	if !strings.HasPrefix(link, "https://example.test/artifacts/public/") || !strings.Contains(link, "sig=") {
		t.Fatalf("url = %q", link)
	}
}
