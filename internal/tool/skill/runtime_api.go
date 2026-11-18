package skill

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	skills "github.com/uvwt/agentdock/internal/skill"
)

type CapabilityItem struct {
	Name          string
	Description   string
	File          string
	SkillRef      string
	SourceType    string
	SourceID      string
	PluginName    string
	ContentDigest string
	FileCount     int
}

// RuntimeCount 只读取已安装索引，不校验 Skill 包、计算 digest 或遍历文件。
// 概览只需要“已安装多少个”，完整可用性与文件信息仍由 RuntimeSkills 负责。
func (s *Service) RuntimeCount() (int, error) {
	names, err := s.state.ListSkills()
	if err != nil {
		return 0, err
	}
	count := len(names)
	if s.plugins == nil {
		return count, nil
	}
	installed, err := s.plugins.List()
	if err != nil {
		return 0, err
	}
	for _, plugin := range installed {
		if plugin.Enabled {
			count += len(plugin.Components.Skills)
		}
	}
	return count, nil
}

func (s *Service) CapabilityItems() ([]CapabilityItem, error) {
	return s.CapabilityItemsContext(context.Background())
}

func (s *Service) CapabilityItemsContext(ctx context.Context) ([]CapabilityItem, error) {
	return s.collectCapabilityItems(ctx, false)
}

func (s *Service) collectCapabilityItems(ctx context.Context, countFiles bool) ([]CapabilityItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names, err := s.state.ListSkills()
	if err != nil {
		return nil, err
	}
	items := make([]CapabilityItem, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolved, release, err := s.Acquire(ctx, ManagedSkillRef(name))
		if err != nil {
			continue
		}
		meta, digest, fileCount, ok, walkErr := inspectManagedSkillPackage(ctx, resolved.Root, name, countFiles)
		release()
		if walkErr != nil {
			return nil, walkErr
		}
		if !ok {
			continue
		}
		items = append(items, CapabilityItem{
			Name: name, Description: strings.TrimSpace(meta.Description),
			File: resolved.SkillRef + "/SKILL.md", SkillRef: resolved.SkillRef,
			SourceType: resolved.SourceType, SourceID: resolved.SourceID,
			ContentDigest: digest,
			FileCount:     fileCount,
		})
	}
	if s.plugins != nil {
		installed, err := s.plugins.List()
		if err != nil {
			return nil, err
		}
		for _, plugin := range installed {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !plugin.Enabled {
				continue
			}
			for _, component := range plugin.Components.Skills {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				resolved, release, err := s.Acquire(ctx, PluginSkillRef(plugin.Name, component.Name))
				if err != nil {
					continue
				}
				meta, metaErr := loadCachedSkillMetadata(resolved.Root)
				var fileCount int
				var filesErr error
				if metaErr == nil && meta.Name == component.Name && countFiles {
					var files []runtimeSkillFile
					files, filesErr = collectRuntimeSkillFiles(filepath.Clean(resolved.Root))
					fileCount = len(files)
				}
				release()
				if metaErr != nil || meta.Name != component.Name || filesErr != nil {
					continue
				}
				items = append(items, CapabilityItem{
					Name: component.Name, Description: strings.TrimSpace(meta.Description),
					File: resolved.SkillRef + "/SKILL.md", SkillRef: resolved.SkillRef,
					SourceType: resolved.SourceType, SourceID: resolved.SourceID,
					PluginName: plugin.Name, ContentDigest: component.ContentDigest,
					FileCount: fileCount,
				})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		if items[i].SourceType != items[j].SourceType {
			return items[i].SourceType < items[j].SourceType
		}
		return items[i].SourceID < items[j].SourceID
	})
	return items, nil
}

func (s *Service) RuntimeSkills() (Result, error) {
	return s.RuntimeSkillsContext(context.Background())
}

func (s *Service) RuntimeSkillsContext(ctx context.Context) (Result, error) {
	capabilities, err := s.collectCapabilityItems(ctx, true)
	if err != nil {
		return nil, err
	}
	skillsResult := make([]map[string]any, 0, len(capabilities))
	for _, capability := range capabilities {
		item := map[string]any{
			"skill": capability.Name, "name": capability.Name, "description": capability.Description,
			"skill_ref": capability.SkillRef, "source_type": capability.SourceType,
			"content_digest": capability.ContentDigest, "file_count": capability.FileCount,
		}
		if capability.PluginName != "" {
			item["plugin_name"] = capability.PluginName
		}
		skillsResult = append(skillsResult, item)
	}
	return Result{"action": "list", "count": len(skillsResult), "skills": skillsResult, "source": runtimeAPISource}, nil
}

type cachedSkillInspection struct {
	sig    string
	meta   skills.SkillMetadata
	digest string
}

const maxCachedSkillInspections = 1024

var (
	skillInspectionCache      sync.Map
	skillInspectionCacheCount atomic.Int32
)

type cachedPluginSkillMeta struct {
	modTimeNano int64
	size        int64
	meta        skills.SkillMetadata
}

var (
	pluginSkillMetaCache      sync.Map
	pluginSkillMetaCacheCount atomic.Int32
)

func loadCachedSkillMetadata(root string) (skills.SkillMetadata, error) {
	docPath := filepath.Join(filepath.Clean(root), "SKILL.md")
	info, err := os.Stat(docPath)
	if err == nil && info.Mode().IsRegular() {
		if raw, ok := pluginSkillMetaCache.Load(docPath); ok {
			cached := raw.(cachedPluginSkillMeta)
			if cached.modTimeNano == info.ModTime().UnixNano() && cached.size == info.Size() {
				return cached.meta, nil
			}
		}
	}
	data, readErr := os.ReadFile(docPath)
	if readErr != nil {
		return skills.SkillMetadata{}, readErr
	}
	meta, parseErr := skills.ParseSkillMetadata(data)
	if parseErr != nil {
		return skills.SkillMetadata{}, parseErr
	}
	if err == nil && info.Mode().IsRegular() && pluginSkillMetaCacheCount.Load() < maxCachedSkillInspections {
		entry := cachedPluginSkillMeta{modTimeNano: info.ModTime().UnixNano(), size: info.Size(), meta: meta}
		if _, loaded := pluginSkillMetaCache.LoadOrStore(docPath, entry); !loaded {
			pluginSkillMetaCacheCount.Add(1)
		} else {
			pluginSkillMetaCache.Store(docPath, entry)
		}
	}
	return meta, nil
}

// inspectManagedSkillPackage combines package validation, tree-fingerprint caching, metadata parsing,
// content digest computation, and file counting into a single filesystem pass.
func inspectManagedSkillPackage(ctx context.Context, root, expectedName string, countFiles bool) (skills.SkillMetadata, string, int, bool, error) {
	cleanRoot := filepath.Clean(root)
	if _, err := os.Stat(filepath.Join(cleanRoot, "agentdock.yaml")); err == nil {
		return skills.SkillMetadata{}, "", 0, false, nil
	} else if !os.IsNotExist(err) {
		return skills.SkillMetadata{}, "", 0, false, nil
	}

	hasher := sha256.New()
	fileCount := 0
	var sizeBuf [16]byte
	valid := true

	walkErr := filepath.WalkDir(cleanRoot, func(path string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			valid = false
			return fs.SkipAll
		}
		if path == cleanRoot {
			return nil
		}
		rel, relErr := filepath.Rel(cleanRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if !entry.IsDir() && entry.Name() == ".agentdock-install.json" {
			valid = false
			return fs.SkipAll
		}
		if entry.IsDir() {
			hasher.Write([]byte("D:"))
			hasher.Write([]byte(rel))
			hasher.Write([]byte{0})
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			valid = false
			return fs.SkipAll
		}
		hasher.Write([]byte("F:"))
		hasher.Write([]byte(rel))
		binary.LittleEndian.PutUint64(sizeBuf[0:8], uint64(info.Size()))
		binary.LittleEndian.PutUint64(sizeBuf[8:16], uint64(info.ModTime().UnixNano()))
		hasher.Write(sizeBuf[:])

		if countFiles && fileCount < runtimeSkillMaxFiles {
			depth := strings.Count(rel, "/") + 1
			if depth <= runtimeSkillMaxDepth+1 && !isPrivateRuntimeSkillFile(rel) {
				fileCount++
			}
		}
		return nil
	})
	if ctx.Err() != nil {
		return skills.SkillMetadata{}, "", 0, false, ctx.Err()
	}
	if walkErr != nil || !valid {
		return skills.SkillMetadata{}, "", 0, false, nil
	}

	treeSig := hex.EncodeToString(hasher.Sum(nil))
	if raw, ok := skillInspectionCache.Load(cleanRoot); ok {
		cached := raw.(cachedSkillInspection)
		if cached.sig == treeSig {
			if cached.meta.Name != expectedName {
				return skills.SkillMetadata{}, "", 0, false, nil
			}
			return cached.meta, cached.digest, fileCount, true, nil
		}
	}

	docData, err := os.ReadFile(filepath.Join(cleanRoot, "SKILL.md"))
	if err != nil {
		return skills.SkillMetadata{}, "", 0, false, nil
	}
	meta, err := skills.ParseSkillMetadata(docData)
	if err != nil {
		return skills.SkillMetadata{}, "", 0, false, nil
	}
	digest, err := managedContentDigest(cleanRoot)
	if err != nil {
		return skills.SkillMetadata{}, "", 0, false, nil
	}

	entry := cachedSkillInspection{sig: treeSig, meta: meta, digest: digest}
	if _, loaded := skillInspectionCache.LoadOrStore(cleanRoot, entry); loaded {
		skillInspectionCache.Store(cleanRoot, entry)
	} else if skillInspectionCacheCount.Add(1) > maxCachedSkillInspections {
		skillInspectionCache.Delete(cleanRoot)
		skillInspectionCacheCount.Add(-1)
	}

	if meta.Name != expectedName {
		return skills.SkillMetadata{}, "", 0, false, nil
	}
	return meta, digest, fileCount, true, nil
}

func (s *Service) RuntimeSkill(skill string) (Result, error) {
	ref := runtimeSkillReference(skill)
	resolved, release, err := s.Acquire(context.Background(), ref)
	if err != nil {
		return nil, err
	}
	defer release()
	doc, err := skills.LoadSkillDocument(resolved.Root)
	if err != nil {
		return nil, skillToolError(err)
	}
	digest, err := runtimeSkillContentDigest(resolved)
	if err != nil {
		return nil, err
	}
	files, err := collectRuntimeSkillFiles(resolved.Root)
	if err != nil {
		return nil, err
	}
	result := Result{
		"action": "inspect", "skill": resolved.Name, "name": doc.Name, "description": doc.Description,
		"skill_ref": resolved.SkillRef, "source_type": resolved.SourceType,
		"content_digest": digest, "document": doc, "files": files,
		"file_count": len(files), "source": runtimeAPISource,
	}
	if resolved.PluginName != "" {
		result["plugin_name"] = resolved.PluginName
	}
	return result, nil
}

func runtimeSkillReference(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "skill://") {
		return value
	}
	return ManagedSkillRef(value)
}

func runtimeSkillContentDigest(resolved ResolvedSkill) (string, error) {
	if resolved.ContentDigest != "" {
		return resolved.ContentDigest, nil
	}
	return managedContentDigest(resolved.Root)
}

func managedContentDigest(root string) (string, error) {
	digest, err := skills.DigestPackageContent(root)
	if err != nil {
		return "", toolErrorDetails("SKILL_PACKAGE_UNAVAILABLE", "failed to digest current managed Skill content", "runtime", map[string]any{"reason": err.Error()})
	}
	return digest, nil
}
