package publicartifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrNotFound 标识目标发布物不存在（含非法或穿越型 ID）。
// 调用方用它把“没找到”映射成可识别的 NOT_FOUND，而不是内部错误。
var ErrNotFound = errors.New("public artifact not found")

// Remove 删除整个发布目录。ID 校验复用 resolveArtifact（IsLocal + 十六进制
// 白名单 + os.Root 防符号链接），非法或不存在统一返回 ErrNotFound：
// 路径校验结果不能泄露成不同的错误形状。
func (s Store) Remove(artifactID string) error {
	if _, _, err := s.resolveArtifact(artifactID); err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	// artifactID 已通过 IsLocal + 32 位十六进制白名单；RemoveAll 不跟随
	// 目录符号链接，越界路径到这里已经不可能。
	if err := os.RemoveAll(filepath.Join(s.Root, artifactID)); err != nil {
		return fmt.Errorf("remove artifact directory: %w", err)
	}
	return nil
}

// Extend 把过期时间改到 now+duration，并且绝不越过 created_at+MaxRetention
// （与 Publish 的 retention() 钳制同一语义）。成功后重写 metadata.json，
// 并按新的过期时间重签链接；旧链接因为签名覆盖 expires 会立即失效。
// baseURL 的传参风格与 List 一致（调用方给出对外公开地址）。
func (s Store) Extend(artifactID string, duration time.Duration, now time.Time, baseURL string) (PublishResult, error) {
	if duration <= 0 {
		return PublishResult{}, errors.New("extend duration must be positive")
	}
	meta, _, err := s.resolveArtifact(artifactID)
	if err != nil {
		return PublishResult{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	now = normalizeNow(now)
	expires := now.Add(duration)
	if max := meta.CreatedAt.Add(MaxRetention); expires.After(max) {
		expires = max
	}
	meta.ExpiresAt = expires
	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return PublishResult{}, err
	}
	if err := os.WriteFile(filepath.Join(s.Root, artifactID, "metadata.json"), encoded, 0o600); err != nil {
		return PublishResult{}, fmt.Errorf("rewrite artifact metadata: %w", err)
	}
	link, err := s.publicURL(baseURL, meta)
	if err != nil {
		return PublishResult{}, err
	}
	return PublishResult{Metadata: meta, URL: link}, nil
}
