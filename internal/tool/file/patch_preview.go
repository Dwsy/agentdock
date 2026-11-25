package file

import (
	"sort"
	"strings"
)

func stagedDiff(staged map[string]stagedPatchFile) (string, diffStats, map[string]diffStats, error) {
	paths := make([]string, 0, len(staged))
	for path := range staged {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var builder strings.Builder
	total := diffStats{}
	byPath := make(map[string]diffStats, len(staged))
	for _, path := range paths {
		file := staged[path]
		oldContent := string(file.Original)
		newContent := ""
		if file.Content != nil {
			newContent = *file.Content
		}
		diff, _, stats, err := unifiedDiffPreview(file.Display, oldContent, newContent, 0, nil)
		if err != nil {
			return "", diffStats{}, nil, err
		}
		byPath[file.Display] = stats
		builder.WriteString(diff)
		if diff != "" && !strings.HasSuffix(diff, "\n") {
			builder.WriteString("\n")
		}
		if stats.FilesChanged > 0 {
			total.FilesChanged++
		}
		total.Insertions += stats.Insertions
		total.Deletions += stats.Deletions
	}
	return builder.String(), total, byPath, nil
}

// addAffectedFileStats 在预览截断前保留逐文件计数，move 同时包含源与目标；
// 调用追踪不应从展示文本反推一份可能残缺的统计。
func addAffectedFileStats(affected []map[string]any, byPath map[string]diffStats) {
	for _, file := range affected {
		path, _ := file["path"].(string)
		stats := byPath[path]
		if destination, _ := file["move_to"].(string); destination != "" && destination != path {
			stats.Insertions += byPath[destination].Insertions
			stats.Deletions += byPath[destination].Deletions
		}
		file["insertions"] = stats.Insertions
		file["deletions"] = stats.Deletions
	}
}

func patchNearbyContext(lines, oldLines []string) []map[string]any {
	if len(lines) == 0 {
		return nil
	}
	needle := ""
	for _, line := range oldLines {
		if strings.TrimSpace(line) != "" {
			needle = strings.TrimSpace(line)
			break
		}
	}
	if needle == "" {
		return []map[string]any{{"line": 1, "context_start_line": 1, "context": firstLines(lines, 20)}}
	}
	for i, line := range lines {
		if strings.Contains(line, needle) {
			return []map[string]any{lineContext(lines, i)}
		}
	}
	return []map[string]any{{"line": 1, "context_start_line": 1, "context": firstLines(lines, 20)}}
}

func patchContextsForMatches(lines []string, indexes []int) []map[string]any {
	out := make([]map[string]any, 0)
	for _, idx := range indexes {
		out = append(out, lineContext(lines, idx))
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func lineContext(lines []string, idx int) map[string]any {
	start := idx - 10
	if start < 0 {
		start = 0
	}
	end := idx + 11
	if end > len(lines) {
		end = len(lines)
	}
	return map[string]any{"line": idx + 1, "context_start_line": start + 1, "context": append([]string(nil), lines[start:end]...)}
}

func firstLines(lines []string, limit int) []string {
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return append([]string(nil), lines...)
}
