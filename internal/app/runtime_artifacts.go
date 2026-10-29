package app

import (
	"context"
	"strings"
	"time"
)

// RuntimeArtifacts 列出当前仍有效的公开文件发布记录。
// 链接由 store 用同一套签名参数生成，和 /artifacts/public/ 实际可下载地址一致。
func (r *Runtime) RuntimeArtifacts(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	publicBase := strings.TrimRight(strings.TrimSpace(r.cfg.OAuthServerURL), "/")
	items, err := r.artifacts.List(time.Now(), publicBase)
	if err != nil {
		return nil, err
	}
	return Result{
		"ok":              true,
		"source":          runtimeAPISource,
		"public_base_url": publicBase,
		"configured":      publicBase != "",
		"artifacts":       items,
		"count":           len(items),
	}, nil
}
