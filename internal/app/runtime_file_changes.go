package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/uvwt/agentdock/internal/observability"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	"github.com/uvwt/agentdock/internal/textutil"
)

// traceFileDiffBytes 限制单个文件变更保留的 diff 预览；工具本身最多返回 64 KiB，
// 历史视图只需要足够定位改动的片段。
const traceFileDiffBytes = 8 << 10

// affectedFile 是受影响文件的统一中间形态：file_edit 的三种返回（单文件结果、
// 结构化 patch 的 {path,operation,move_to}、unified diff 的 {path,status}）
// 都先归一到这里，历史层不再关心工具返回的方言。
type affectedFile struct {
	path          string
	action        string
	moveTo        string
	insertions    int
	deletions     int
	hasInsertions bool
	hasDeletions  bool
}

// traceFileChanges 从 file_edit 的返回结果提取文件级变更。
// 只在成功且实际写入的调用上产生数据：失败调用的 diff 不可信，dry_run/未变更
// 的调用没有落盘改动，都不应出现在历史里。
func traceFileChanges(tool string, args map[string]any, result *Result, success bool) []observability.FileChange {
	if tool != "file_edit" || !success || result == nil {
		return nil
	}
	payload := *result
	if dryRun, _ := payload["dry_run"].(bool); dryRun {
		return nil
	}
	if changed, ok := payload["changed"].(bool); ok && !changed {
		return nil
	}

	// file_edit 把完整编辑结果放在进程内键里，模型侧只收到精简摘要。
	// 历史层优先读取完整副本；没有该键时仍兼容直接调用工具的旧路径。
	if history, ok := payload[toolfile.FileEditHistoryKey].(toolcore.Result); ok {
		payload = map[string]any(history)
	}
	diff, _ := payload["diff_preview"].(string)
	truncated, _ := payload["truncated"].(bool)
	preview := textutil.SafeTruncateString(diff, traceFileDiffBytes)
	if preview.Truncated {
		truncated = true
	}

	callAction, _ := payload["action"].(string)
	if callAction == "" {
		callAction, _ = args["action"].(string)
	}

	files := traceAffectedFiles(payload["affected_files"], callAction)
	if len(files) == 0 {
		// 单文件 action（replace/add/delete/move）不带 affected_files。
		path, _ := payload["path"].(string)
		if path == "" {
			return nil
		}
		entry := affectedFile{path: path, action: normalizeFileAction(callAction)}
		if entry.action == "move" {
			entry.moveTo, _ = payload["new_path"].(string)
		}
		files = []affectedFile{entry}
	}

	// 旧结果缺少逐文件计数时按 diff 回退，不能让历史正文的 8 KiB 上限
	// 吞掉后续文件。旧结果自身已截断时统计只是下界；新结果优先完整计数。
	stats := diffFileStats(diff)
	callInsertions := traceIntValue(payload["insertions"])
	callDeletions := traceIntValue(payload["deletions"])
	_, hasInsertions := payload["insertions"]
	_, hasDeletions := payload["deletions"]

	changes := make([]observability.FileChange, 0, len(files))
	for _, file := range files {
		insertions, deletions := callInsertions, callDeletions
		stat, _ := lookupDiffStats(stats, file.path)
		if file.moveTo != "" && file.moveTo != file.path {
			destination, _ := lookupDiffStats(stats, file.moveTo)
			stat[0] += destination[0]
			stat[1] += destination[1]
		}
		if file.hasInsertions {
			insertions = file.insertions
		} else if len(files) > 1 || !hasInsertions {
			insertions = stat[0]
		}
		if file.hasDeletions {
			deletions = file.deletions
		} else if len(files) > 1 || !hasDeletions {
			deletions = stat[1]
		}
		changes = append(changes, observability.FileChange{
			Path:        file.path,
			Action:      file.action,
			MoveTo:      file.moveTo,
			ProjectRoot: traceProjectRoot(file.path),
			Insertions:  insertions,
			Deletions:   deletions,
			Diff:        preview.Text,
			Truncated:   truncated,
		})
	}
	return changes
}

// traceCodeChangeCounts 提取 file_edit 调用级的增删行数。
// 调用分析用它统计代码行数变动：与历史层的逐文件统计不同，这里要的是
// 一次调用的总量，且不依赖 trace 开关（trace 关闭时统计依然要成立）。
func traceCodeChangeCounts(tool string, result *Result, success bool) (int, int) {
	if tool != "file_edit" || !success || result == nil {
		return 0, 0
	}
	payload := *result
	if history, ok := payload[toolfile.FileEditHistoryKey].(toolcore.Result); ok {
		payload = map[string]any(history)
	}
	if dryRun, _ := payload["dry_run"].(bool); dryRun {
		return 0, 0
	}
	if changed, ok := payload["changed"].(bool); ok && !changed {
		return 0, 0
	}
	return traceIntValue(payload["insertions"]), traceIntValue(payload["deletions"])
}

// traceAffectedFiles 在 JSON 边界归一化 affected_files：进程内工具会返回
// []map 或私有结构体切片，经过传输后才是 []any；两条路径必须产生相同摘要。
func traceAffectedFiles(value any, callAction string) []affectedFile {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var entries []any
	if err := json.Unmarshal(encoded, &entries); err != nil {
		return nil
	}
	out := make([]affectedFile, 0, len(entries))
	seen := make(map[affectedFile]bool, len(entries))
	for _, item := range entries {
		var file affectedFile
		switch entry := item.(type) {
		case string:
			file = affectedFile{path: entry, action: normalizeFileAction(callAction)}
		case map[string]any:
			file.path, _ = entry["path"].(string)
			rawAction, _ := entry["operation"].(string)
			if rawAction == "" {
				rawAction, _ = entry["status"].(string)
			}
			if rawAction == "" {
				rawAction = callAction
			}
			file.action = normalizeFileAction(rawAction)
			file.moveTo, _ = entry["move_to"].(string)
			file.insertions = traceIntValue(entry["insertions"])
			file.deletions = traceIntValue(entry["deletions"])
			_, file.hasInsertions = entry["insertions"]
			_, file.hasDeletions = entry["deletions"]
		}
		if file.path != "" && !seen[file] {
			out = append(out, file)
			seen[file] = true
		}
	}
	return out
}

// normalizeFileAction 把工具返回的动词方言归一到历史词汇表：
// create / edit / delete / move。UI 文案与筛选都基于这四个词。
func normalizeFileAction(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "add", "added", "create", "created":
		return "create"
	case "delete", "deleted", "remove", "removed":
		return "delete"
	case "move", "moved", "rename", "renamed":
		return "move"
	default:
		// replace / patch / update / modified / 未知动词都是“改内容”。
		return "edit"
	}
}

// lookupDiffStats 匹配分节统计：diff 头里的路径可能是工作目录相对路径，
// 而受影响文件列表是展示路径，先精确匹配再按后缀对齐。
func lookupDiffStats(stats map[string][2]int, path string) ([2]int, bool) {
	if stat, ok := stats[path]; ok {
		return stat, true
	}
	var matched [2]int
	longest := 0
	for key, stat := range stats {
		if len(key) > longest && strings.HasSuffix(path, "/"+key) {
			matched, longest = stat, len(key)
		}
	}
	return matched, longest > 0
}

// diffFileStats 按 unified diff 分节统计每个文件的增删行数，复用 textutil
// 的分节解析：envelope patch 的预览只有 ---/+++ 头没有 diff --git 行，
// 旧的逐行扫描会把整个多文件 patch 统计成全 0。预览被截断时统计是下界，
// 调用方已有 truncated 标记如实提示。
func diffFileStats(diffText string) map[string][2]int {
	stats := map[string][2]int{}
	for _, section := range textutil.DiffSections(diffText) {
		stats[section.Path] = [2]int{section.Insertions, section.Deletions}
	}
	return stats
}

// traceProjectMarkers 是 IDE 语义的项目标记；命中任意一个即认为该目录是项目根。
var traceProjectMarkers = []string{
	".git", ".hg", ".svn",
	"go.mod", "package.json", "Cargo.toml", "pyproject.toml",
	"pom.xml", "build.gradle", "build.gradle.kts",
}

// traceProjectRoot 从文件路径向上找项目标记，找不到回退父目录。
// 只做文件系统元数据探测，任何失败都不影响历史记录本身。
func traceProjectRoot(path string) string {
	dir := filepath.Dir(path)
	for range 24 {
		for _, marker := range traceProjectMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Dir(path)
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
