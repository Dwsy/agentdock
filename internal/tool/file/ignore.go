package file

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type ignoreMatcher struct {
	root       string
	exclude    []gitignoreRule
	files      map[string][]gitignoreRule
	dirIgnored map[string]bool
}

type gitignoreRule struct {
	domain   []string
	pattern  string
	negate   bool
	dirOnly  bool
	rooted   bool
	hasSlash bool
}

func loadIgnoreMatcher(root string) *ignoreMatcher {
	matcher := &ignoreMatcher{
		root:       root,
		files:      make(map[string][]gitignoreRule),
		dirIgnored: make(map[string]bool),
	}
	matcher.exclude = loadIgnoreRules(resolveGitInfoExclude(root), nil)
	return matcher
}

func (m *ignoreMatcher) Ignored(rel string, isDir bool) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "" {
		return false
	}
	if isDir {
		if cached, ok := m.dirIgnored[rel]; ok {
			return cached
		}
	}
	// Git 不允许通过子路径的否定规则重新包含一个已被忽略目录里的文件。
	// Memoizing directory decisions turns deep tree walks from O(depth^2) into O(1) parent hits.
	if slash := strings.LastIndexByte(rel, '/'); slash >= 0 {
		if m.Ignored(rel[:slash], true) {
			if isDir {
				m.dirIgnored[rel] = true
			}
			return true
		}
	}
	parts := strings.Split(rel, "/")
	ignored := m.pathIgnored(rel, parts, isDir)
	if isDir {
		m.dirIgnored[rel] = ignored
	}
	return ignored
}

func (m *ignoreMatcher) pathIgnored(rel string, parts []string, isDir bool) bool {
	ignored := false
	for _, rule := range m.exclude {
		if rule.matches(parts, isDir) {
			ignored = !rule.negate
		}
	}
	prefixLen := 0
	for depth := 0; depth < len(parts); depth++ {
		domainKey := ""
		if depth > 0 {
			domainKey = rel[:prefixLen]
		}
		for _, rule := range m.ignoreFile(domainKey, parts[:depth]) {
			if rule.matches(parts, isDir) {
				ignored = !rule.negate
			}
		}
		if depth == 0 {
			prefixLen = len(parts[0])
		} else {
			prefixLen += 1 + len(parts[depth])
		}
	}
	return ignored
}

func (m *ignoreMatcher) ignoreFile(key string, domain []string) []gitignoreRule {
	if rules, ok := m.files[key]; ok {
		return rules
	}

	parts := make([]string, 0, len(domain)+2)
	parts = append(parts, m.root)
	parts = append(parts, domain...)
	parts = append(parts, ".gitignore")
	rules := loadIgnoreRules(filepath.Join(parts...), domain)
	m.files[key] = rules
	return rules
}

func loadIgnoreRules(path string, domain []string) []gitignoreRule {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	rules := make([]gitignoreRule, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if rule, ok := parseGitignoreRule(scanner.Text(), domain); ok {
			rules = append(rules, rule)
		}
	}
	return rules
}

func parseGitignoreRule(line string, domain []string) (gitignoreRule, bool) {
	line = strings.TrimSuffix(line, "\r")
	line = trimGitignoreTrailingSpaces(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return gitignoreRule{}, false
	}

	rule := gitignoreRule{domain: append([]string(nil), domain...)}
	if strings.HasPrefix(line, `\#`) {
		line = line[1:]
	}
	if strings.HasPrefix(line, "!") {
		rule.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\!`) {
		line = line[1:]
	}
	if line == "" {
		return gitignoreRule{}, false
	}

	rule.dirOnly = strings.HasSuffix(line, "/")
	if rule.dirOnly {
		line = strings.TrimSuffix(line, "/")
	}
	rule.rooted = strings.HasPrefix(line, "/")
	if rule.rooted {
		line = strings.TrimPrefix(line, "/")
	}
	if line == "" {
		return gitignoreRule{}, false
	}
	rule.pattern = line
	rule.hasSlash = strings.Contains(line, "/")
	return rule, true
}

func trimGitignoreTrailingSpaces(line string) string {
	for strings.HasSuffix(line, " ") {
		backslashes := 0
		for i := len(line) - 2; i >= 0 && line[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 == 1 {
			break
		}
		line = strings.TrimSuffix(line, " ")
	}
	return line
}

func (r gitignoreRule) matches(parts []string, isDir bool) bool {
	if len(parts) <= len(r.domain) {
		return false
	}
	for i, component := range r.domain {
		if parts[i] != component {
			return false
		}
	}
	relParts := parts[len(r.domain):]
	if r.rooted || r.hasSlash {
		if r.dirOnly && !isDir {
			return false
		}
		return matchGitignorePattern(r.pattern, strings.Join(relParts, "/"))
	}

	for i, component := range relParts {
		componentIsDir := i < len(relParts)-1 || isDir
		if r.dirOnly && !componentIsDir {
			continue
		}
		if matchGitignorePattern(r.pattern, component) {
			return true
		}
	}
	return false
}

func matchGitignorePattern(pattern, candidate string) bool {
	// Git 中 trailing /** 匹配目录内部内容，不匹配该目录本身；doublestar
	// 会把二者都视为匹配，因此这里补齐 Git 的这一处语义差异。
	if strings.HasSuffix(pattern, "/**") && candidate == strings.TrimSuffix(pattern, "/**") {
		return false
	}
	return globMatch(pattern, candidate)
}

func resolveGitInfoExclude(root string) string {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return filepath.Join(gitPath, "info", "exclude")
	}

	data, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	gitDir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	gitDir = filepath.Clean(gitDir)

	commonDir := gitDir
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = strings.TrimSpace(string(data))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
		commonDir = filepath.Clean(commonDir)
	}
	return filepath.Join(commonDir, "info", "exclude")
}
