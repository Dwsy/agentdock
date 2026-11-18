package app

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	skills "github.com/uvwt/agentdock/internal/skill"
)

const (
	filesystemSkillIndexLimit       = 50
	filesystemSkillDescriptionBytes = 120
	filesystemSkillDocumentMaxBytes = 1 << 20
	maxCachedCommonSkillDocs        = 4096
)

type cachedCommonSkillDoc struct {
	modTimeNano int64
	size        int64
	item        filesystemSkillItem
	ok          bool
}

var (
	commonSkillDocCache      sync.Map
	commonSkillDocCacheCount atomic.Int32
)

type filesystemSkillItem struct {
	Name        string
	Description string
	File        string
}

type filesystemSkillIndex struct {
	Items     []filesystemSkillItem
	Total     int
	Truncated bool
}

// scanCommonFilesystemSkills 保留全局 common Skill 的历史行为：
// package 目录和 SKILL.md 都允许通过 symlink 访问。
func scanCommonFilesystemSkills(root string) (filesystemSkillIndex, error) {
	cleanRoot := filepath.Clean(root)
	entries, err := os.ReadDir(cleanRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return filesystemSkillIndex{Items: []filesystemSkillItem{}}, nil
		}
		return filesystemSkillIndex{}, err
	}

	items := make([]filesystemSkillItem, 0, min(len(entries), filesystemSkillIndexLimit))
	for _, entry := range entries {
		item, ok := loadCachedCommonSkillItem(cleanRoot, entry.Name())
		if ok {
			items = append(items, item)
		}
	}
	index := buildFilesystemSkillIndex(items)
	// common Skills 来自数量不可控的共享目录，继续保留历史的逐项 description 预算。
	for i := range index.Items {
		index.Items[i].Description = truncateString(index.Items[i].Description, filesystemSkillDescriptionBytes)
	}
	return index, nil
}

func loadCachedCommonSkillItem(root, entryName string) (filesystemSkillItem, bool) {
	packageDir := filepath.Join(root, entryName)
	info, err := os.Stat(packageDir)
	if err != nil || !info.IsDir() {
		return filesystemSkillItem{}, false
	}
	documentPath := filepath.Join(packageDir, "SKILL.md")
	docInfo, err := os.Stat(documentPath)
	if err != nil || !docInfo.Mode().IsRegular() || docInfo.Size() > filesystemSkillDocumentMaxBytes {
		return filesystemSkillItem{}, false
	}
	modNano := docInfo.ModTime().UnixNano()
	size := docInfo.Size()

	if raw, ok := commonSkillDocCache.Load(documentPath); ok {
		cached := raw.(cachedCommonSkillDoc)
		if cached.modTimeNano == modNano && cached.size == size {
			return cached.item, cached.ok
		}
	}

	data, readErr := readCommonFilesystemSkillDocument(packageDir)
	var entry cachedCommonSkillDoc
	entry.modTimeNano = modNano
	entry.size = size
	if readErr == nil {
		if metadata, parseErr := skills.ParseSkillMetadata(data); parseErr == nil && metadata.Name == entryName {
			entry.item = filesystemSkillItem{
				Name:        metadata.Name,
				Description: strings.TrimSpace(metadata.Description),
				File:        documentPath,
			}
			entry.ok = true
		}
	}
	if _, loaded := commonSkillDocCache.LoadOrStore(documentPath, entry); loaded {
		commonSkillDocCache.Store(documentPath, entry)
	} else if commonSkillDocCacheCount.Add(1) > maxCachedCommonSkillDocs {
		commonSkillDocCache.Delete(documentPath)
		commonSkillDocCacheCount.Add(-1)
	}
	return entry.item, entry.ok
}

// scanWorkspaceFilesystemSkills 在扫描 .agents/skills 期间始终持有 workspace Root。
// 所有路径都从该 Root 相对解析，因此父目录 symlink 或 Windows reparse point
// 不能把扫描重定向到所选 workspace 之外。common Skill 继续走独立的历史兼容入口。
func scanWorkspaceFilesystemSkills(workspaceRoot string) (filesystemSkillIndex, error) {
	root, err := os.OpenRoot(workspaceRoot)
	if err != nil {
		return filesystemSkillIndex{}, err
	}
	defer root.Close()

	const skillRoot = ".agents/skills"
	dir, err := root.Open(filepath.FromSlash(skillRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return filesystemSkillIndex{Items: []filesystemSkillItem{}}, nil
		}
		return filesystemSkillIndex{}, err
	}
	entries, err := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err != nil {
		return filesystemSkillIndex{}, err
	}
	if closeErr != nil {
		return filesystemSkillIndex{}, closeErr
	}

	relativeSkillRoot := filepath.FromSlash(skillRoot)
	return indexFilesystemSkills(entries, func(entry os.DirEntry) (string, []byte, error) {
		packagePath := filepath.Join(relativeSkillRoot, entry.Name())
		info, err := root.Lstat(packagePath)
		if err != nil || !info.IsDir() {
			return "", nil, os.ErrInvalid
		}

		documentPath := filepath.Join(packagePath, "SKILL.md")
		before, err := root.Lstat(documentPath)
		if err != nil || !before.Mode().IsRegular() {
			return "", nil, os.ErrInvalid
		}
		file, err := root.Open(documentPath)
		if err != nil {
			return "", nil, err
		}
		defer file.Close()
		after, err := file.Stat()
		if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
			return "", nil, os.ErrInvalid
		}
		data, err := readBoundedSkillDocument(file)
		if err != nil {
			return "", nil, err
		}
		return filepath.Join(workspaceRoot, documentPath), data, nil
	})
}

func indexFilesystemSkills(entries []os.DirEntry, load func(os.DirEntry) (string, []byte, error)) (filesystemSkillIndex, error) {
	items := make([]filesystemSkillItem, 0, len(entries))
	for _, entry := range entries {
		documentPath, data, err := load(entry)
		if err != nil {
			continue
		}
		metadata, parseErr := skills.ParseSkillMetadata(data)
		if parseErr != nil {
			continue
		}
		if metadata.Name != entry.Name() {
			continue
		}
		items = append(items, filesystemSkillItem{
			Name:        metadata.Name,
			Description: strings.TrimSpace(metadata.Description),
			File:        documentPath,
		})
	}

	return buildFilesystemSkillIndex(items), nil
}

func buildFilesystemSkillIndex(items []filesystemSkillItem) filesystemSkillIndex {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].File < items[j].File
		}
		return items[i].Name < items[j].Name
	})
	index := filesystemSkillIndex{Items: items, Total: len(items)}
	if index.Total > filesystemSkillIndexLimit {
		index.Truncated = true
		index.Items = index.Items[:filesystemSkillIndexLimit]
	}
	return index
}

func readCommonFilesystemSkillDocument(packageDir string) ([]byte, error) {
	file, err := os.Open(filepath.Join(packageDir, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readBoundedSkillDocument(file)
}

func readBoundedSkillDocument(file *os.File) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, filesystemSkillDocumentMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > filesystemSkillDocumentMaxBytes {
		return nil, os.ErrInvalid
	}
	return data, nil
}
