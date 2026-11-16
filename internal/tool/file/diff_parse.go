package file

import (
	"strings"

	"github.com/uvwt/agentdock/internal/textutil"
)

type gitDiffFile struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	Binary     bool   `json:"binary"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

// parseDiffFiles 解析统一 diff 的文件级元数据，既用于 Git diff，也用于 file_edit patch 预览。
// git diff 走 diff --git 头；纯 unified diff（无 diff --git 行）回退到
// ---/+++ 文件头对，否则 git apply 一个普通 unified diff 时 affected_files
// 为空，历史层会整条丢弃该变更。
func parseDiffFiles(diffText string) []gitDiffFile {
	files := parseGitDiffFiles(diffText)
	if len(files) == 0 {
		files = parseUnifiedDiffFiles(diffText)
	}
	byPath := make(map[string]textutil.DiffSection)
	for _, section := range textutil.DiffSections(diffText) {
		byPath[section.Path] = section
	}
	for i := range files {
		stats := byPath[files[i].Path]
		files[i].Insertions, files[i].Deletions = stats.Insertions, stats.Deletions
	}
	return files
}

func parseGitDiffFiles(diffText string) []gitDiffFile {
	files := make([]gitDiffFile, 0)
	current := -1
	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			path := ""
			if len(parts) >= 4 {
				path = strings.TrimPrefix(parts[3], "b/")
			}
			files = append(files, gitDiffFile{Path: path, Status: "modified"})
			current = len(files) - 1
			continue
		}
		if current < 0 {
			continue
		}
		if strings.HasPrefix(line, "new file mode") {
			files[current].Status = "added"
		}
		if strings.HasPrefix(line, "deleted file mode") {
			files[current].Status = "deleted"
		}
		if strings.HasPrefix(line, "Binary files") {
			files[current].Binary = true
		}
	}
	return files
}

// parseUnifiedDiffFiles 从 ---/+++ 文件头对提取纯 unified diff 的文件列表；
// /dev/null 侧推断 added/deleted 状态。
func parseUnifiedDiffFiles(diffText string) []gitDiffFile {
	files := make([]gitDiffFile, 0)
	lines := strings.Split(diffText, "\n")
	for i := 0; i+2 < len(lines); i++ {
		// 与 DiffSections 同一判据：---/+++ 后必须紧跟 @@，避免把
		// 删除 "-- x"/新增 "++ x" 渲染出的内容行对误当文件头。
		if !strings.HasPrefix(lines[i], "--- ") || !strings.HasPrefix(lines[i+1], "+++ ") || !strings.HasPrefix(lines[i+2], "@@") {
			continue
		}
		oldPath := textutil.DiffHeaderPath(lines[i][4:])
		newPath := textutil.DiffHeaderPath(lines[i+1][4:])
		path, status := newPath, "modified"
		if path == "" {
			path, status = oldPath, "deleted"
		} else if oldPath == "" {
			status = "added"
		}
		if path == "" {
			continue
		}
		files = append(files, gitDiffFile{Path: path, Status: status})
		i++ // 跳过 +++ 行
	}
	return files
}

func truncateBytes(data []byte, maxBytes int) (string, bool) {
	truncated := textutil.SafeTruncateBytes(data, maxBytes)
	return truncated.Text, truncated.Truncated
}
