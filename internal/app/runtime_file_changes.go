package app

import (
	"encoding/json"

	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/textutil"
)

// traceFileDiffBytes 限制单个文件变更保留的 diff 预览；工具本身最多返回 64 KiB，
// 历史视图只需要足够定位改动的片段。
const traceFileDiffBytes = 8 << 10

// traceFileChanges 从 file_edit 的返回结果提取文件级变更。
// 只在成功调用上产生数据：失败调用的 diff 不可信，也不应出现在历史里。
func traceFileChanges(tool string, args map[string]any, result *Result, success bool) []observability.FileChange {
	if tool != "file_edit" || !success || result == nil {
		return nil
	}
	payload := *result

	diff, _ := payload["diff_preview"].(string)
	truncated, _ := payload["truncated"].(bool)
	preview := textutil.SafeTruncateBytes([]byte(diff), traceFileDiffBytes)
	if preview.Truncated {
		truncated = true
	}

	action, _ := payload["action"].(string)
	if action == "" {
		action, _ = args["action"].(string)
	}
	insertions := traceIntValue(payload["insertions"])
	deletions := traceIntValue(payload["deletions"])

	paths := traceStringSlice(payload["affected_files"])
	if len(paths) == 0 {
		if path, _ := payload["path"].(string); path != "" {
			paths = []string{path}
		}
	}
	if len(paths) == 0 {
		return nil
	}

	changes := make([]observability.FileChange, 0, len(paths))
	for _, path := range paths {
		changes = append(changes, observability.FileChange{
			Path:       path,
			Action:     action,
			Insertions: insertions,
			Deletions:  deletions,
			Diff:       preview.Text,
			Truncated:  truncated,
		})
	}
	return changes
}

func traceIntValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return 0
}

func traceStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}
