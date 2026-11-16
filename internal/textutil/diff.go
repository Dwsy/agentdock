package textutil

import "strings"

// DiffSection 是 unified diff 中一个文件分节的路径与增删行数。
// Path 是去掉 a/、b/ 前缀后的展示路径，与工具返回的 affected_files 对齐。
type DiffSection struct {
	Path       string
	Insertions int
	Deletions  int
}

// DiffSections 按文件分节统计 unified diff 的增删行数，同时覆盖两种格式：
// git diff（diff --git 头）与纯 unified diff（---/+++ 文件头对，envelope
// patch 的 stagedDiffPreview 只产这种）。分节切换依赖 "--- 紧跟 +++ 紧跟 @@"
// 的文件头形态：hunk 内容行删除 "-- x" 渲染为 "--- x"、新增 "++ x" 渲染为
// "+++ x"，只有三行成组出现才判定为新分节，孤立行按内容计数。
func DiffSections(diffText string) []DiffSection {
	if diffText == "" {
		return nil
	}
	lines := strings.Split(diffText, "\n")
	sections := make([]DiffSection, 0)
	index := map[string]int{}
	current := -1
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "--- ") && i+2 < len(lines) &&
			strings.HasPrefix(lines[i+1], "+++ ") && strings.HasPrefix(lines[i+2], "@@"):
			path := DiffHeaderPath(lines[i+1][4:])
			if path == "" {
				path = DiffHeaderPath(line[4:])
			}
			if path == "" {
				current = -1
				continue
			}
			if existing, ok := index[path]; ok {
				current = existing
			} else {
				sections = append(sections, DiffSection{Path: path})
				current = len(sections) - 1
				index[path] = current
			}
			i++ // 文件头对消费 +++ 行
		case current >= 0 && strings.HasPrefix(line, "+"):
			sections[current].Insertions++
		case current >= 0 && strings.HasPrefix(line, "-"):
			sections[current].Deletions++
		}
	}
	return sections
}

// DiffHeaderPath 提取 unified diff 文件头行（--- / +++ 之后）的路径：
// 去掉 a/、b/ 前缀与制表时间戳；/dev/null（新增或删除文件的另一侧）返回空串。
func DiffHeaderPath(field string) string {
	if tab := strings.IndexByte(field, '\t'); tab >= 0 {
		field = field[:tab]
	}
	field = strings.TrimSpace(field)
	if field == "" || field == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(field, "a/"), "b/")
}
