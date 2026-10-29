package publicartifacts

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// List 返回当前仍然有效的发布记录（新到旧）。
// 过期目录留给 Cleanup 处理，这里不列出，避免控制面板显示已不可下载的链接。
func (s Store) List(now time.Time, baseURL string) ([]PublishResult, error) {
	now = normalizeNow(now)
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []PublishResult{}, nil
		}
		return nil, err
	}
	results := make([]PublishResult, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, metaErr := readMetadataPath(filepath.Join(s.Root, entry.Name(), "metadata.json"))
		if metaErr != nil || !meta.ExpiresAt.After(now) {
			continue
		}
		link, linkErr := s.publicURL(baseURL, meta)
		if linkErr != nil {
			continue
		}
		results = append(results, PublishResult{Metadata: meta, URL: link})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].CreatedAt.Equal(results[j].CreatedAt) {
			return results[i].ArtifactID < results[j].ArtifactID
		}
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
	return results, nil
}
