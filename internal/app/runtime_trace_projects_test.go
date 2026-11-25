package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
)

func TestRuntimeTraceProjectPaths(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	root := rt.ws.Root()
	project := filepath.Join(root, "project")
	nested := filepath.Join(project, "nested")
	for _, dir := range []string{filepath.Join(project, "assets"), filepath.Join(nested, "files")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"project/go.mod", "project/assets/sample.txt", "project/nested/package.json", "project/nested/files/sample.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("needle\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "assets", "sample.png"), imageData.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"read_file", map[string]any{"path": "project/assets/sample.txt"}},
		{"list_dir", map[string]any{"path": "project/nested"}},
		{"search_text", map[string]any{"path": "project/nested/files/sample.txt", "query": "needle"}},
		{"view_image", map[string]any{"path": "project/assets/sample.png"}},
	} {
		if _, err := rt.Call(context.Background(), call.tool, call.args); err != nil {
			t.Fatalf("call %s: %v", call.tool, err)
		}
	}
	imageID := rt.traces.Query(observability.TraceQuery{Tool: "view_image", Limit: 1})[0].ID
	unclassified := rt.traces.Record(observability.TraceRecord{Tool: "view_image", Arguments: `{"path":"project","artifact_id":"project/assets/a.png","url":"https://example.org/a","message":"/project/assets"}`, Result: `{"source":{"type":"artifact","path":"project/assets/a.png"}}`})
	workdir := rt.traces.Record(observability.TraceRecord{
		Tool: "exec_command", Command: &observability.CommandTrace{Workdir: filepath.Join(project, "assets"), Stdout: "private output"},
		Arguments:   `{"cwd":"project/nested","path":"project/nested/files/sample.txt"}`,
		FileChanges: []observability.FileChange{{ProjectRoot: nested, Diff: "private diff"}},
	})

	query := observability.TraceQuery{Summary: true, Projects: true, Cursor: true, Limit: 3}
	first, err := rt.RuntimeTraces(query)
	if err != nil {
		t.Fatal(err)
	}
	if first["has_more"] != true || !reflect.DeepEqual(first["project_paths"], map[uint64]string{workdir: project, imageID: project}) {
		t.Fatalf("first page projects = %#v, has_more = %v", first["project_paths"], first["has_more"])
	}
	firstRecords := first["records"].([]observability.TraceRecord)
	if names := first["file_names"]; !reflect.DeepEqual(names, map[uint64][]string{}) {
		t.Fatalf("first page file names = %#v", names)
	}
	if firstRecords[1].ID != unclassified || firstRecords[0].Command.Stdout != "" || firstRecords[0].FileChanges[0].Diff != "" {
		t.Fatalf("first page did not preserve summary: %#v", firstRecords)
	}
	query.BeforeID = imageID
	second, err := rt.RuntimeTraces(query)
	if err != nil {
		t.Fatal(err)
	}
	secondRecords := second["records"].([]observability.TraceRecord)
	want := map[uint64]string{secondRecords[0].ID: nested, secondRecords[1].ID: nested, secondRecords[2].ID: project}
	if second["has_more"] != false || !reflect.DeepEqual(second["project_paths"], want) {
		t.Fatalf("second page projects = %#v, want %#v, has_more = %v", second["project_paths"], want, second["has_more"])
	}
	if names := second["file_names"]; !reflect.DeepEqual(names, map[uint64][]string{secondRecords[2].ID: {"sample.txt"}}) {
		t.Fatalf("second page file names = %#v", names)
	}
	for _, record := range append(firstRecords, secondRecords...) {
		if record.Arguments != "" || record.Result != "" {
			t.Fatalf("record %d exposed a body", record.ID)
		}
	}
	detail, _ := rt.traces.RecordByID(workdir)
	if detail.Arguments == "" || detail.Command.Stdout == "" || detail.FileChanges[0].Diff == "" {
		t.Fatal("summary changed stored detail")
	}

	for _, query := range []observability.TraceQuery{{Summary: true}, {Projects: true}} {
		legacy, err := rt.RuntimeTraces(query)
		_, hasProjects := legacy["project_paths"]
		_, hasFileNames := legacy["file_names"]
		if err != nil || hasProjects || hasFileNames {
			t.Fatalf("legacy or full view: project_paths = %v, file_names = %v, error = %v", hasProjects, hasFileNames, err)
		}
	}
	for _, runtime := range []*Runtime{rt, {}} {
		empty, err := runtime.RuntimeTraces(observability.TraceQuery{Summary: true, Projects: true, Tool: "missing"})
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"project_paths", "file_names"} {
			encoded, err := json.Marshal(empty[key])
			if err != nil || string(encoded) != "{}" {
				t.Fatalf("empty %s = %s, error = %v", key, encoded, err)
			}
		}
	}
}

func TestRuntimeTraceFileNames(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	if err := os.WriteFile(filepath.Join(rt.ws.Root(), "review.rs"), []byte(strings.Repeat("private content\n", observability.TracePreviewBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Call(context.Background(), "read_file", map[string]any{"path": "review.rs"}); err != nil {
		t.Fatal(err)
	}
	read := rt.traces.Query(observability.TraceQuery{Tool: "read_file", Limit: 1})[0]
	if !read.Truncated || tracePreviewPathFields(read.Result).text("path") != "" {
		t.Fatal("expected the long read to lose its result path")
	}
	if _, err := rt.Call(context.Background(), "read_file", map[string]any{"path": "missing.rs"}); err == nil {
		t.Fatal("expected read of missing file to fail")
	}
	failed := rt.traces.Query(observability.TraceQuery{Tool: "read_file", Limit: 1})[0]
	if failed.Success || failed.ID == read.ID {
		t.Fatal("expected a separate failed read trace")
	}
	if err := os.WriteFile(filepath.Join(rt.ws.Root(), "a.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt.ws.Root(), "b.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Call(context.Background(), "file_edit", map[string]any{
		"action": "patch", "workdir": rt.ws.Root(), "patch": patchTraceFixtures()["envelope"], "dry_run": true,
	}); err != nil {
		t.Fatal(err)
	}
	dryRun := rt.traces.Query(observability.TraceQuery{Tool: "file_edit", Limit: 1})[0]
	if len(dryRun.FileChanges) != 0 {
		t.Fatal("dry run unexpectedly recorded file changes")
	}
	patchID := rt.traces.Record(observability.TraceRecord{
		Tool: "file_edit", Success: true,
		Arguments: `{"action":"patch","patch":"*** Update File: ignored.rs"}`,
		Result:    `{"path":"ignored.rs"}`,
		FileChanges: []observability.FileChange{
			{Path: "src/old.rs", MoveTo: `D:\lib\renamed.rs`, Action: "move", Diff: "private diff"},
			{Path: "/work/src/new.rs", Action: "create"},
			{Path: "/work/folder/", Action: "edit"},
		},
	})
	result, err := rt.RuntimeTraces(observability.TraceQuery{Summary: true, Projects: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint64][]string{
		read.ID:   {"review.rs"},
		failed.ID: {"missing.rs"},
		dryRun.ID: {"a.txt", "b.txt"},
		patchID:   {"renamed.rs", "new.rs"},
	}
	if names := result["file_names"]; !reflect.DeepEqual(names, want) {
		t.Fatalf("file_names = %#v, want %#v", names, want)
	}
	for _, record := range result["records"].([]observability.TraceRecord) {
		if record.Arguments != "" || record.Result != "" || (record.ID == patchID && record.FileChanges[0].Diff != "") {
			t.Fatalf("summary leaked a body for trace %d", record.ID)
		}
	}
}

func TestTraceFileNamesFromRetainedFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record observability.TraceRecord
		want   []string
	}{
		{"read resolved path", observability.TraceRecord{Tool: "read_file", Result: `{"path":"/work/resolved.rs","content":"unfinished`, Arguments: `{"path":"alias.rs"}`}, []string{"resolved.rs"}},
		{"skill display path", observability.TraceRecord{Tool: "read_file", Result: `{"path":"skill://managed/example/file%20.md"}`}, []string{"file%20.md"}},
		{"skill request URI", observability.TraceRecord{Tool: "read_file", Arguments: `{"path":"skill://managed/example/file%20name.md"}`}, []string{"file name.md"}},
		{"Windows UNC", observability.TraceRecord{Tool: "read_file", Arguments: `{"path":"\\\\server\\share\\review.rs"}`}, []string{"review.rs"}},
		{"Windows drive-relative", observability.TraceRecord{Tool: "read_file", Arguments: `{"path":"D:review.rs"}`}, []string{"review.rs"}},
		{"single edit", observability.TraceRecord{Tool: "file_edit", Arguments: `{"action":"replace","path":"Makefile"}`}, []string{"Makefile"}},
		{"single move", observability.TraceRecord{Tool: "file_edit", Result: `{"action":"move","path":"old.rs"}`, Arguments: `{"path":"old.rs","new_path":"dst/new.rs"}`}, []string{"new.rs"}},
		{"dry run patch", observability.TraceRecord{Tool: "file_edit", Result: `{"action":"patch","affected_files":["src/first.rs",{"path":"old.rs","operation":"move","move_to":"dst/second.rs"}],"diff_preview":"unfinished`}, []string{"first.rs", "second.rs"}},
		{"incomplete patch list", observability.TraceRecord{Tool: "file_edit", Result: `{"affected_files":[{"path":"one.rs"},`}, nil},
		{"body only", observability.TraceRecord{Tool: "file_edit", Arguments: `{"patch":"*** Update File: src/hidden.rs"}`, Result: `{"diff_preview":"+++ b/hidden.rs","output":{"path":"hidden.rs"}}`}, nil},
		{"redacted and incomplete", observability.TraceRecord{Tool: "read_file", Result: `{"path":"/work/[REDACTED]/review.rs"}`, Arguments: `{"path":"/work/incomplete.rs`}, nil},
		{"directory path", observability.TraceRecord{Tool: "read_file", Arguments: `{"path":"src/"}`}, nil},
		{"known directory", observability.TraceRecord{Tool: "read_file", ErrorCode: "IS_DIRECTORY", Arguments: `{"path":"src"}`}, nil},
		{"drive root", observability.TraceRecord{Tool: "read_file", Arguments: `{"path":"D:"}`}, nil},
		{"UNC share root", observability.TraceRecord{Tool: "read_file", Arguments: `{"path":"\\\\server\\share"}`}, nil},
		{"URL", observability.TraceRecord{Tool: "read_file", Arguments: `{"path":"https://example.org/review.rs"}`}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := traceFileNames(tc.record); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("file names = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestTraceProjectPathStructuredFields(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	root := rt.ws.Root()
	for _, name := range []string{"alpha", "alpha/beta", "one"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, body, want string }{
		{"explicit directory", `{"directory":"./alpha/beta"}`, filepath.Join(root, "alpha", "beta")},
		{"explicit file", `{"file_path":"./alpha/beta"}`, filepath.Join(root, "alpha")},
		{"stable priority", `{"cwd":"./two","workdir":"./one","file_path":"./three/a"}`, filepath.Join(root, "one")},
		{"wrong field type", `{"cwd":[],"directory":"./alpha/beta"}`, filepath.Join(root, "alpha", "beta")},
		{"bare directory", `{"directory":"alpha"}`, filepath.Join(root, "alpha")},
		{"path list", `{"paths":["./alpha/beta/"]}`, filepath.Join(root, "alpha", "beta")},
		{"complete field before truncation", `{"cwd":"./alpha/beta","text":"incomplete`, filepath.Join(root, "alpha", "beta")},
		{"truncated path", `{"cwd":"./alpha/`, ""},
		{"redacted path", `{"file_path":"./alpha/[REDACTED]/file"}`, ""},
		{"unrelated nested path", `{"output":{"path":"./alpha/beta"}}`, ""},
		{"plain slug", `{"directory":"a-slug"}`, ""},
		{"URL", `{"file_path":"https://example.org/file"}`, ""},
		{"scheme-relative URL", `{"file_path":"//example.org/file"}`, ""},
		{"artifact URI", `{"file_path":"artifact://123/file"}`, ""},
		{"artifact download path", `{"file_path":"/artifacts/public/123/file"}`, ""},
		{"foreign drive", `{"file_path":"D:\\work\\file"}`, "D:/work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rt.traceProjectPath(observability.TraceRecord{Arguments: tc.body})
			if filepath.ToSlash(got) != filepath.ToSlash(tc.want) {
				t.Fatalf("project path = %q, want %q", got, tc.want)
			}
		})
	}
	delegated := observability.TraceRecord{Tool: "mcp_tool_call", Arguments: `{"arguments":{"cwd":"alpha/beta"}}`}
	if got, want := rt.traceProjectPath(delegated), filepath.Join(root, "alpha", "beta"); got != want {
		t.Fatalf("delegated project = %q, want %q", got, want)
	}
}
