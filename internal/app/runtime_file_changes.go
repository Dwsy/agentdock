package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/textutil"
)

// traceFileDiffBytes 限制单个文件变更保留的 diff 预览；工具本身最多返回 64 KiB，
// 历史视图只需要足够定位改动的片段。
const traceFileDiffBytes = 8 << 10

// affectedFile 是受影响文件的统一中间形态：file_edit 的三种返回（单文件结果、
// 结构化 patch 的 {path,operation,move_to}、unified diff 的 {path,status}）
// 都先归一到这里，历史层不再关心工具返回的方言。
type affectedFile struct {
	path   string
	action string
	moveTo string
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

	diff, _ := payload["diff_preview"].(string)
	truncated, _ := payload["truncated"].(bool)
	preview := textutil.SafeTruncateBytes([]byte(diff), traceFileDiffBytes)
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

	// 多文件 patch 的调用级 +/- 是总数，冒充单文件统计会误导；按 diff 分节
	// 重新统计，截断预览统计不到的文件如实记 0（预览已标 truncated）。
	stats := diffFileStats(preview.Text)
	callInsertions := traceIntValue(payload["insertions"])
	callDeletions := traceIntValue(payload["deletions"])

	changes := make([]observability.FileChange, 0, len(files))
	for _, file := range files {
		insertions, deletions := callInsertions, callDeletions
		if len(files) > 1 {
			insertions, deletions = 0, 0
			if stat, ok := lookupDiffStats(stats, file.path); ok {
				insertions, deletions = stat[0], stat[1]
			}
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
	if dryRun, _ := payload["dry_run"].(bool); dryRun {
		return 0, 0
	}
	if changed, ok := payload["changed"].(bool); ok && !changed {
		return 0, 0
	}
	return traceIntValue(payload["insertions"]), traceIntValue(payload["deletions"])
}

// traceAffectedFiles 解析 affected_files 的三种形态；无法识别时返回 nil，
// 由调用方回退到单文件 path。
func traceAffectedFiles(value any, callAction string) []affectedFile {
	switch typed := value.(type) {
	case []string:
		out := make([]affectedFile, 0, len(typed))
		for _, path := range typed {
			if path != "" {
				out = append(out, affectedFile{path: path, action: normalizeFileAction(callAction)})
			}
		}
		return out
	case []any:
		out := make([]affectedFile, 0, len(typed))
		for _, item := range typed {
			switch entry := item.(type) {
			case string:
				if entry != "" {
					out = append(out, affectedFile{path: entry, action: normalizeFileAction(callAction)})
				}
			case map[string]any:
				path, _ := entry["path"].(string)
				if path == "" {
					continue
				}
				rawAction, _ := entry["operation"].(string)
				if rawAction == "" {
					rawAction, _ = entry["status"].(string)
				}
				if rawAction == "" {
					rawAction = callAction
				}
				moveTo, _ := entry["move_to"].(string)
				out = append(out, affectedFile{path: path, action: normalizeFileAction(rawAction), moveTo: moveTo})
			}
		}
		return out
	}
	return nil
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
	for key, stat := range stats {
		if strings.HasSuffix(path, "/"+key) {
			return stat, true
		}
	}
	return [2]int{}, false
}

// diffFileStats 按 unified diff 分节统计每个文件的增删行数。
// 预览被截断时统计是下界，调用方已有 truncated 标记如实提示。
func diffFileStats(diffText string) map[string][2]int {
	stats := map[string][2]int{}
	if diffText == "" {
		return stats
	}
	current := ""
	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			current = ""
			if len(parts) >= 4 {
				current = strings.TrimPrefix(parts[3], "b/")
			}
			continue
		}
		if current == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"):
			stat := stats[current]
			stat[0]++
			stats[current] = stat
		case strings.HasPrefix(line, "-"):
			stat := stats[current]
			stat[1]++
			stats[current] = stat
		}
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
