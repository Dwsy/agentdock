package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/agentinstructions"
)

func TestRuntimeCatalogExposesEveryToolSchema(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	result, err := rt.RuntimeCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	tools, ok := result["tools"].([]catalogTool)
	if !ok {
		t.Fatalf("catalog tools type = %T", result["tools"])
	}
	if len(tools) != len(rt.ToolNames()) {
		t.Fatalf("catalog tools = %d, registered = %d", len(tools), len(rt.ToolNames()))
	}
	if result["tool_count"] != len(tools) {
		t.Fatalf("tool_count = %v", result["tool_count"])
	}
	for _, tool := range tools {
		if tool.Name == "" || tool.Description == "" {
			t.Fatalf("tool metadata incomplete: %#v", tool)
		}
		if len(tool.InputSchema) == 0 || len(tool.OutputSchema) == 0 {
			t.Fatalf("tool %s is missing a schema", tool.Name)
		}
	}

	if _, ok := result["rules"].([]agentinstructions.File); !ok {
		t.Fatalf("catalog rules type = %T", result["rules"])
	}
	if _, ok := result["workdir"].(string); !ok {
		t.Fatalf("catalog workdir = %#v", result["workdir"])
	}

	// 控制面板按 JSON 消费，字段名必须是稳定的 snake_case。
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsAll(t, string(encoded), `"input_schema"`, `"output_schema"`, `"tool_count"`, `"workspace_root"`)
}

func TestRuntimeCatalogReportsConfiguredToolSet(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	result, err := rt.RuntimeCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tools := result["tools"].([]catalogTool)
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		seen[tool.Name] = true
	}
	for _, required := range []string{"agentdock_context", "workspace_context", "exec_command", "task_create"} {
		if !seen[required] {
			t.Fatalf("catalog is missing %s", required)
		}
	}
}

func assertContainsAll(t *testing.T, body string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(body, needle) {
			t.Fatalf("body is missing %q: %s", needle, body)
		}
	}
}
