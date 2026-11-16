package observability

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

const (
	traceImagePreviewMaxBytes    = 2 << 20
	traceImagePreviewMemoryBytes = 16 << 20
)

// TraceImagePreview 是图片 sidecar 的轻量索引，不包含图片字节。
type TraceImagePreview struct {
	MimeType  string `json:"mime_type"`
	SizeBytes int    `json:"size_bytes"`
}

type traceImagePreviewData struct {
	data     []byte
	mimeType string
}

// storeImagePreviewLocked 在分配 trace ID 后保存图片。disk 模式使用私有原子文件；
// memory 模式使用有总量上限的缓存。调用方必须持有 s.mu。
func (s *TraceStore) storeImagePreviewLocked(id uint64, startedAt time.Time, data []byte, mimeType string) *TraceImagePreview {
	if id == 0 || len(data) == 0 || len(data) > traceImagePreviewMaxBytes {
		return nil
	}
	if traceImageExtension(mimeType) == "" {
		return nil
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	switch s.mode {
	case TraceModeMemory:
		if len(data) > traceImagePreviewMemoryBytes {
			return nil
		}
		if s.imagePreviews == nil {
			s.imagePreviews = make(map[uint64]traceImagePreviewData)
		}
		for s.imagePreviewBytes+len(data) > traceImagePreviewMemoryBytes {
			var oldest uint64
			for candidate := range s.imagePreviews {
				if oldest == 0 || candidate < oldest {
					oldest = candidate
				}
			}
			if oldest == 0 {
				return nil
			}
			s.imagePreviewBytes -= len(s.imagePreviews[oldest].data)
			delete(s.imagePreviews, oldest)
		}
		s.imagePreviews[id] = traceImagePreviewData{
			data:     append([]byte(nil), data...),
			mimeType: mimeType,
		}
		s.imagePreviewBytes += len(data)
	case TraceModeDisk:
		path := s.imagePreviewPath(id, startedAt, mimeType)
		if path == "" || atomicfile.Write(path, data, 0o600) != nil {
			return nil
		}
	default:
		return nil
	}

	return &TraceImagePreview{MimeType: mimeType, SizeBytes: len(data)}
}

// ImagePreview 返回保留窗口内记录的图片字节。磁盘模式只在用户打开详情时读取。
func (s *TraceStore) ImagePreview(id uint64) ([]byte, string, bool) {
	if s == nil || id == 0 {
		return nil, "", false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for index := len(s.records) - 1; index >= 0; index-- {
		record := s.records[index]
		if record.ID != id || record.ImagePreview == nil {
			continue
		}
		if s.mode == TraceModeMemory {
			preview, ok := s.imagePreviews[id]
			if !ok {
				return nil, "", false
			}
			return append([]byte(nil), preview.data...), preview.mimeType, true
		}
		if s.mode == TraceModeDisk {
			path := s.imagePreviewPath(id, record.StartedAt, record.ImagePreview.MimeType)
			if path == "" {
				return nil, "", false
			}
			data, err := os.ReadFile(path)
			if err != nil || len(data) != record.ImagePreview.SizeBytes {
				return nil, "", false
			}
			return data, record.ImagePreview.MimeType, true
		}
	}
	return nil, "", false
}

func (s *TraceStore) imagePreviewDir() string {
	return filepath.Join(s.root, "images")
}

func (s *TraceStore) imagePreviewPath(id uint64, startedAt time.Time, mimeType string) string {
	extension := traceImageExtension(mimeType)
	if s.root == "" || id == 0 || extension == "" {
		return ""
	}
	if startedAt.IsZero() {
		return ""
	}
	return filepath.Join(s.imagePreviewDir(), traceDay(startedAt), strconv.FormatUint(id, 10)+extension)
}

func traceImageExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	default:
		return ""
	}
}

// removeImagePreviewLocked 清理被环淘汰记录的图片。调用方必须持有 s.mu。
func (s *TraceStore) removeImagePreviewLocked(record TraceRecord) {
	if record.ImagePreview == nil {
		return
	}
	if s.mode == TraceModeMemory {
		if preview, ok := s.imagePreviews[record.ID]; ok {
			s.imagePreviewBytes -= len(preview.data)
			delete(s.imagePreviews, record.ID)
		}
		return
	}
	if s.mode == TraceModeDisk {
		path := s.imagePreviewPath(record.ID, record.StartedAt, record.ImagePreview.MimeType)
		if path != "" {
			_ = os.Remove(path)
			_ = os.Remove(filepath.Dir(path))
		}
	}
}

func (s *TraceStore) clearImagePreviewsLocked() error {
	if s.mode != TraceModeDisk || s.root == "" {
		return nil
	}
	if err := os.RemoveAll(s.imagePreviewDir()); err != nil {
		return fmt.Errorf("remove trace image previews: %w", err)
	}
	return nil
}

// cleanImagePreviews 删除早于 trace retention 的按日 sidecar 目录。
func (s *TraceStore) cleanImagePreviews(cutoff string) {
	if s == nil || s.mode != TraceModeDisk || s.root == "" {
		return
	}
	entries, err := os.ReadDir(s.imagePreviewDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			_ = os.Remove(filepath.Join(s.imagePreviewDir(), entry.Name()))
			continue
		}
		if cutoff == "" || entry.Name() < cutoff {
			_ = os.RemoveAll(filepath.Join(s.imagePreviewDir(), entry.Name()))
		}
	}
}

// cleanImagePreviewsForRecords 清理已超出内存保留窗口、无法再通过 trace ID 读取的图片。
func (s *TraceStore) cleanImagePreviewsForRecords() {
	if s == nil || s.mode != TraceModeDisk || s.root == "" {
		return
	}
	keep := make(map[string]struct{})
	for _, record := range s.records {
		if record.ImagePreview == nil {
			continue
		}
		if path := s.imagePreviewPath(record.ID, record.StartedAt, record.ImagePreview.MimeType); path != "" {
			keep[path] = struct{}{}
		}
	}

	entries, err := os.ReadDir(s.imagePreviewDir())
	if err != nil {
		return
	}
	for _, day := range entries {
		if !day.IsDir() {
			continue
		}
		dayPath := filepath.Join(s.imagePreviewDir(), day.Name())
		files, err := os.ReadDir(dayPath)
		if err != nil {
			continue
		}
		for _, file := range files {
			extension := strings.ToLower(filepath.Ext(file.Name()))
			if extension != ".jpg" && extension != ".png" {
				continue
			}
			path := filepath.Join(dayPath, file.Name())
			if _, ok := keep[path]; !ok {
				_ = os.Remove(path)
			}
		}
		_ = os.Remove(dayPath)
	}
}
