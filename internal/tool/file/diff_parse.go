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
	inHunk := false
	for _, line := range strings.Split(diffText, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if header, ok := strings.CutPrefix(line, "diff --git "); ok {
			files = append(files, gitDiffFile{Path: gitDiffHeaderPath(header), Status: "modified"})
			current = len(files) - 1
			inHunk = false
			continue
		}
		if strings.HasPrefix(line, "@@") {
			inHunk = true
		}
		if current < 0 || inHunk {
			continue
		}
		file := &files[current]
		switch {
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			if path := textutil.DiffHeaderPath(line[4:]); path != "" {
				file.Path = path
			}
		case strings.HasPrefix(line, "rename to "):
			file.Path = textutil.UnquoteGitPath(line[len("rename to "):])
		case strings.HasPrefix(line, "copy to "):
			file.Path = textutil.UnquoteGitPath(line[len("copy to "):])
		case strings.HasPrefix(line, "new file mode"):
			file.Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			file.Status = "deleted"
		case strings.HasPrefix(line, "Binary files"), line == "GIT binary patch":
			file.Binary = true
		}
	}
	return files
}

func gitDiffHeaderPath(header string) string {
	if strings.HasPrefix(header, `"`) {
		for i := 1; i < len(header); i++ {
			switch header[i] {
			case '\\':
				i++
			case '"':
				destination := strings.TrimPrefix(header[i+1:], " ")
				return strings.TrimPrefix(textutil.UnquoteGitPath(destination), "b/")
			}
		}
		return ""
	}
	if i := strings.Index(header, ` "b/`); i >= 0 {
		return strings.TrimPrefix(textutil.UnquoteGitPath(header[i+1:]), "b/")
	}
	// Mode-only and binary modifications can omit ---/+++ and use the same path on both sides.
	fallback := ""
	for start := 0; start < len(header); {
		i := strings.Index(header[start:], " b/")
		if i < 0 {
			break
		}
		i += start
		destination := header[i+3:]
		if strings.TrimPrefix(header[:i], "a/") == destination {
			return destination
		}
		if start == 0 {
			fallback = destination
		}
		start = i + 1
	}
	return fallback
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
