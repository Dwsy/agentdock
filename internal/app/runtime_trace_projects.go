package app

import (
	"encoding/json"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/uvwt/agentdock/internal/observability"
)

// Only explicitly named fields participate in attribution. Other fields can
// contain file contents, search matches, URLs, and artifacts.
type tracePathFields map[string]json.RawMessage

func (fields tracePathFields) text(key string) string {
	var value string
	_ = json.Unmarshal(fields[key], &value)
	return value
}

// Retained previews may end mid-field. A complete field before that point is
// still usable, while an incomplete string or object must never become a path.
func tracePreviewPathFields(body string) tracePathFields {
	decoder := json.NewDecoder(strings.NewReader(body))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	fields := make(tracePathFields)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			break
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			break
		}
		fields[key.(string)] = value
	}
	return fields
}

func traceFileNames(record observability.TraceRecord) []string {
	if (record.Tool != "read_file" && record.Tool != "file_edit") || record.ErrorCode == "IS_DIRECTORY" {
		return nil
	}
	if record.Tool == "file_edit" {
		var names []string
		for _, change := range record.FileChanges {
			if name := traceFileBaseName(change.MoveTo, change.Path); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			return names
		}
	}

	result := tracePreviewPathFields(record.Result)
	args := tracePreviewPathFields(record.Arguments)
	resultPath, requestPath := result.text("path"), args.text("path")
	if record.Tool == "read_file" {
		// Skill results already contain the decoded display path. Requests use a URI.
		resultPath = strings.TrimPrefix(resultPath, "skill://")
		if strings.HasPrefix(requestPath, "skill://") {
			resource, err := url.Parse(requestPath)
			if err == nil && resource.User == nil && resource.RawQuery == "" && resource.Fragment == "" && resource.Port() == "" {
				requestPath = resource.Path
			}
		}
	} else {
		action := result.text("action")
		if action == "" {
			action = args.text("action")
		}
		// Dry runs can retain explicit patch targets without recording file changes.
		if affected := result["affected_files"]; len(affected) > 0 {
			var names []string
			for _, file := range traceAffectedFiles(affected, action) {
				if name := traceFileBaseName(file.moveTo, file.path); name != "" {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				return names
			}
		}
		if action == "" || normalizeFileAction(action) == "move" {
			if name := traceFileBaseName(result.text("new_path"), args.text("new_path")); name != "" {
				return []string{name}
			}
		}
	}
	if name := traceFileBaseName(resultPath, requestPath); name != "" {
		return []string{name}
	}
	return nil
}

func traceFileBaseName(paths ...string) string {
	for _, raw := range paths {
		if strings.Contains(raw, "[REDACTED]") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
			continue
		}
		slash := strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
		if strings.HasSuffix(slash, "/") || strings.Contains(slash, "://") || strings.HasPrefix(slash, "/artifacts/public/") {
			continue
		}
		// A UNC volume without a path names a share, and a drive prefix
		// can also appear on a drive-relative path (C:filename).
		if strings.HasPrefix(slash, "//") && strings.Count(slash[2:], "/") < 2 {
			continue
		}
		if len(slash) >= 2 && slash[1] == ':' && ((slash[0] >= 'A' && slash[0] <= 'Z') || (slash[0] >= 'a' && slash[0] <= 'z')) {
			slash = slash[2:]
		}
		if slash == "" {
			continue
		}
		if name := path.Base(slash); name != "." && name != ".." && name != "~" {
			return name
		}
	}
	return ""
}

type tracePathKind int

const (
	traceUnknownPath tracePathKind = iota
	traceDirectoryPath
	traceFilePath
)

func (r *Runtime) traceProjectPath(record observability.TraceRecord) string {
	cwd := r.ws.DefaultCWD()
	if record.Command != nil {
		if project := projectForTracePath(record.Command.Workdir, cwd, traceDirectoryPath); project != "" {
			return project
		}
	}
	for _, change := range record.FileChanges {
		if root := traceLocalPath(change.ProjectRoot, cwd); root != "" {
			return root
		}
	}

	// Prefer resolved paths in tool results. Relative results are relative to
	// the workspace root, while requests use the default working directory.
	for _, preview := range []struct{ body, base string }{
		{record.Result, r.ws.Root()}, {record.Arguments, cwd},
	} {
		fields := tracePreviewPathFields(preview.body)
		if root := projectFromTraceFields(fields, preview.base, record.Tool); root != "" {
			return root
		}
		if record.Tool == "mcp_tool_call" {
			var args tracePathFields
			if json.Unmarshal(fields["arguments"], &args) == nil {
				if root := projectFromTraceFields(args, preview.base, record.Tool); root != "" {
					return root
				}
			}
		}
	}
	return ""
}

func projectFromTraceFields(fields tracePathFields, base, tool string) string {
	if tool == "view_image" && (fields.text("artifact_id") != "" || fields.text("url") != "") {
		return ""
	}
	if root := traceLocalPath(fields.text("project_root"), base); root != "" {
		return root
	}
	for _, key := range []string{"workdir", "cwd", "directory", "dir", "root"} {
		if root := projectForTracePath(fields.text(key), base, traceDirectoryPath); root != "" {
			return root
		}
	}
	var source tracePathFields
	if json.Unmarshal(fields["source"], &source) == nil && source.text("type") == "path" {
		if root := projectForTracePath(source.text("path"), base, traceFilePath); root != "" {
			return root
		}
	}
	if root := projectForTracePath(fields.text("file_path"), base, traceFilePath); root != "" {
		return root
	}
	kind := traceToolPathKind(tool, fields.text("action"))
	for _, key := range []string{"path", "new_path", "file", "local_path", "mount_path"} {
		if root := projectForTracePath(fields.text(key), base, kind); root != "" {
			return root
		}
	}
	var paths []string
	if json.Unmarshal(fields["paths"], &paths) == nil {
		for _, raw := range paths {
			if root := projectForTracePath(raw, base, kind); root != "" {
				return root
			}
		}
	}
	return ""
}

func traceToolPathKind(tool, action string) tracePathKind {
	switch tool {
	case "list_dir":
		return traceDirectoryPath
	case "read_file", "view_image":
		return traceFilePath
	case "file_edit":
		if action == "add" || action == "replace" {
			return traceFilePath
		}
	}
	return traceUnknownPath
}

func projectForTracePath(raw, base string, kind tracePathKind) string {
	local := traceLocalPath(raw, base)
	if local == "" {
		// A bare name is a directory or file only when it exists at the base.
		name := strings.TrimSpace(raw)
		if name != "" && !strings.ContainsAny(name, `/\:`) && !strings.Contains(name, "[REDACTED]") && strings.IndexFunc(name, unicode.IsControl) < 0 {
			candidate := filepath.Join(base, name)
			if info, err := os.Stat(candidate); err == nil && (info.IsDir() || kind != traceDirectoryPath) {
				local = candidate
			}
		}
	}
	if local == "" {
		return ""
	}
	if kind == traceUnknownPath && strings.HasSuffix(strings.ReplaceAll(raw, `\`, "/"), "/") {
		kind = traceDirectoryPath
	}
	// Foreign drive paths and UNC paths can be grouped lexically. Avoid probing
	// the local filesystem with a foreign path or opening a network share.
	if !filepath.IsAbs(local) || strings.HasPrefix(local, `\\`) {
		switch kind {
		case traceDirectoryPath:
			return local
		case traceFilePath:
			slash := strings.ReplaceAll(local, `\`, "/")
			dir := path.Dir(slash)
			if len(dir) == 2 && dir[1] == ':' {
				dir += "/"
			}
			if strings.HasPrefix(slash, "//") {
				dir = "/" + dir
			}
			return dir
		default:
			return ""
		}
	}
	if kind == traceUnknownPath {
		info, err := os.Stat(local)
		if err != nil {
			return ""
		}
		if info.IsDir() {
			kind = traceDirectoryPath
		}
	}
	if kind != traceDirectoryPath {
		local = filepath.Dir(local)
	}
	return traceDirectoryProjectRoot(local)
}

func traceLocalPath(raw, base string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "[REDACTED]") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return ""
	}
	slash := strings.ReplaceAll(raw, `\`, "/")
	drive := len(slash) >= 3 && slash[1:3] == ":/" &&
		((slash[0] >= 'A' && slash[0] <= 'Z') || (slash[0] >= 'a' && slash[0] <= 'z'))
	if (strings.Contains(slash, ":") && !drive) || strings.Contains(slash, "://") ||
		strings.HasPrefix(raw, "//") || strings.HasPrefix(slash, "/artifacts/public/") {
		return ""
	}
	if strings.HasPrefix(raw, `\\`) {
		return raw
	}
	if !strings.Contains(slash, "/") && slash != "." && slash != ".." && slash != "~" {
		return ""
	}
	if slash == "~" || strings.HasPrefix(slash, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, strings.TrimPrefix(slash[1:], "/"))
	}
	if drive && !filepath.IsAbs(filepath.FromSlash(slash)) {
		return slash[:3] + strings.TrimPrefix(path.Clean(slash[2:]), "/")
	}
	if absolute := filepath.FromSlash(slash); filepath.IsAbs(absolute) {
		return filepath.Clean(absolute)
	}
	return filepath.Join(base, filepath.FromSlash(slash))
}
