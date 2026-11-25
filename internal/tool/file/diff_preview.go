package file

import (
	"bytes"
	"fmt"

	anchoreddiff "github.com/rogpeppe/go-internal/diff"
	"github.com/uvwt/agentdock/internal/textutil"
)

const maxDiffOutputBytes = 64 << 20

type diffStats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

func unifiedDiffPreview(path, oldContent, newContent string, maxBytes int, capture func(string)) (string, bool, diffStats, error) {
	output := anchoreddiff.Diff("a/"+path, []byte(oldContent), "b/"+path, []byte(newContent))
	if len(output) > 0 {
		// 该实现会额外输出 diff 命令头；工具契约只返回 unified diff。
		if newline := bytes.IndexByte(output, '\n'); newline >= 0 {
			output = output[newline+1:]
		}
	}
	if len(output) > maxDiffOutputBytes {
		return "", false, diffStats{}, fmt.Errorf("diff output exceeds %d bytes (observed %d bytes)", maxDiffOutputBytes, len(output))
	}
	diff := string(output)
	stats := countDiffStats(diff)
	preview := editDiffPreview(diff, maxBytes, capture)
	return preview.Text, preview.Truncated, stats, nil
}

func editDiffPreview(diff string, maxBytes int, capture func(string)) textutil.Truncation {
	if capture != nil && diff != "" {
		capture(diff)
	}
	return textutil.SafeTruncateString(diff, maxBytes)
}

// countDiffStats 按分节统计整个 diff 文本的增删行数。用分节解析而不是
// 逐行扫描：内容行删除 "-- x" 渲染为 "--- x"，逐行扫描会把它误当文件头
// 漏计；FilesChanged 是有变更的分节数，多文件 git patch 才能报出真实文件数。
func countDiffStats(diffText string) diffStats {
	stats := diffStats{}
	for _, section := range textutil.DiffSections(diffText) {
		if section.Insertions > 0 || section.Deletions > 0 {
			stats.FilesChanged++
		}
		stats.Insertions += section.Insertions
		stats.Deletions += section.Deletions
	}
	return stats
}
