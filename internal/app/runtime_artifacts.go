package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

// MaxDashboardPublishBytes 限制控制台单次上传发布的原始文件体积（20 MiB）；
// 更大文件或目录走本机路径发布，不占用控制面请求内存。
const MaxDashboardPublishBytes int64 = 20 << 20

// ArtifactPublishInput 描述控制面发起的一次文件发布请求（路径与上传字节二选一）。
type ArtifactPublishInput struct {
	Path             string
	Filename         string
	MimeType         string
	Data             []byte
	RetentionSeconds int
}

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

// RuntimeArtifactRevoke 撤销（删除）一个发布物。不存在与非法 ID 都映射成
// NOT_FOUND：路径校验结果不能泄露成不同错误形状。
func (r *Runtime) RuntimeArtifactRevoke(id string) (Result, error) {
	startedAt := time.Now()
	artifactID := strings.TrimSpace(id)
	args := map[string]any{"artifact_id": artifactID}
	err := r.artifacts.Remove(artifactID)
	if err != nil {
		if errors.Is(err, publicartifacts.ErrNotFound) {
			err = toolError("NOT_FOUND", "published artifact not found", "not_found")
		}
		r.observeArtifactMutation("revoke", artifactID, startedAt, args, nil, err)
		return nil, err
	}
	result := Result{"ok": true, "source": runtimeAPISource, "action": "revoke", "artifact_id": artifactID}
	r.observeArtifactMutation("revoke", artifactID, startedAt, args, result, nil)
	return result, nil
}

// RuntimeArtifactExtend 把发布物有效期延长 hours 小时（1..720），并且绝不
// 越过 created_at+MaxRetention。返回重签后的完整发布结果。
func (r *Runtime) RuntimeArtifactExtend(id string, hours int) (Result, error) {
	startedAt := time.Now()
	artifactID := strings.TrimSpace(id)
	args := map[string]any{"artifact_id": artifactID, "hours": hours}
	if hours < 1 || hours > 720 {
		err := toolErrorDetails("INVALID_ARGUMENT", "artifact extension must be between 1 and 720 hours", "validation", map[string]any{"hours": hours, "minimum": 1, "maximum": 720})
		r.observeArtifactMutation("extend", artifactID, startedAt, args, nil, err)
		return nil, err
	}
	publicBase := strings.TrimRight(strings.TrimSpace(r.cfg.OAuthServerURL), "/")
	extended, err := r.artifacts.Extend(artifactID, time.Duration(hours)*time.Hour, time.Now(), publicBase)
	if err != nil {
		if errors.Is(err, publicartifacts.ErrNotFound) {
			err = toolError("NOT_FOUND", "published artifact not found", "not_found")
		}
		r.observeArtifactMutation("extend", artifactID, startedAt, args, nil, err)
		return nil, err
	}
	result := Result{"ok": true, "source": runtimeAPISource, "action": "extend", "artifact": extended}
	r.observeArtifactMutation("extend", artifactID, startedAt, args, result, nil)
	return result, nil
}

// RuntimeArtifactPublish 从控制面发布本机路径（文件或目录）或上传的文件字节。
// 敏感路径（agentdock.env 与 secrets/oauth 目录）直接拒绝，上传载荷只把文件名与大小记入 trace。
func (r *Runtime) RuntimeArtifactPublish(ctx context.Context, input ArtifactPublishInput) (Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	startedAt := time.Now()
	rawPath := strings.TrimSpace(input.Path)
	rawName := strings.TrimSpace(input.Filename)
	hasPath := rawPath != ""
	hasUpload := len(input.Data) > 0 || rawName != ""

	traceArgs := map[string]any{"retention_seconds": input.RetentionSeconds}
	if hasPath {
		traceArgs["path"] = rawPath
	}
	if hasUpload {
		traceName := rawName
		if traceName == "" {
			traceName = "uploaded-file"
		}
		traceArgs["filename"] = traceName
		traceArgs["size_bytes"] = len(input.Data)
	}

	fail := func(err error) (Result, error) {
		r.observeArtifactMutation("publish", "", startedAt, traceArgs, nil, err)
		return nil, err
	}

	maxRetentionSec := int(publicartifacts.MaxRetention / time.Second)
	if input.RetentionSeconds < 0 || input.RetentionSeconds > maxRetentionSec {
		return fail(toolErrorDetails("INVALID_ARGUMENT", "retention_seconds must be between 0 and 604800", "validation", map[string]any{
			"retention_seconds": input.RetentionSeconds, "minimum": 0, "maximum": maxRetentionSec,
		}))
	}
	if hasPath && hasUpload {
		return fail(toolError("INVALID_ARGUMENT", "specify either path or uploaded file, not both", "validation"))
	}
	if !hasPath && !hasUpload {
		return fail(toolError("INVALID_ARGUMENT", "path or uploaded file is required", "validation"))
	}
	if hasUpload && len(input.Data) == 0 {
		return fail(toolError("INVALID_ARGUMENT", "uploaded file must not be empty", "validation"))
	}
	if hasUpload && int64(len(input.Data)) > MaxDashboardPublishBytes {
		return fail(toolError("INVALID_ARGUMENT", "uploaded file exceeds 20 MiB limit", "validation"))
	}

	publicBase := strings.TrimRight(strings.TrimSpace(r.cfg.OAuthServerURL), "/")
	var (
		published publicartifacts.PublishResult
		err       error
	)
	if hasPath {
		if r.ws == nil {
			return fail(toolError("INVALID_ARGUMENT", "workspace resolver is unavailable", "validation"))
		}
		resolved, resolveErr := r.ws.ResolveExisting(rawPath)
		if resolveErr != nil {
			if errors.Is(resolveErr, os.ErrNotExist) {
				return fail(toolError("NOT_FOUND", "publish source path not found", "not_found"))
			}
			return fail(toolError("INVALID_ARGUMENT", resolveErr.Error(), "validation"))
		}
		if isBlockedPublishPath(resolved.Abs, r.cfg.AgentDockHome) {
			return fail(toolError("INVALID_ARGUMENT", "refusing to publish internal AgentDock secret or configuration file", "validation"))
		}
		published, err = r.artifacts.Publish(publicartifacts.PublishRequest{
			Path:             resolved.Abs,
			RetentionSeconds: input.RetentionSeconds,
			BaseURL:          publicBase,
		})
	} else {
		name := rawName
		if name == "" {
			name = "uploaded-file"
		}
		published, err = r.artifacts.PublishBytes(publicartifacts.PublishBytesRequest{
			Filename:         name,
			Data:             input.Data,
			MimeType:         strings.TrimSpace(input.MimeType),
			RetentionSeconds: input.RetentionSeconds,
			BaseURL:          publicBase,
		})
	}
	if err != nil {
		return fail(err)
	}
	result := Result{"ok": true, "source": runtimeAPISource, "action": "publish", "artifact": published}
	r.observeArtifactMutation("publish", published.ArtifactID, startedAt, traceArgs, result, nil)
	return result, nil
}

// RuntimeArtifactOpen 按发布物 ID 打开经过完整性与过期校验的只读文件句柄。
// 不存在、非法 ID 或已过期统一映射为 NOT_FOUND；调用方负责关闭文件。
func (r *Runtime) RuntimeArtifactOpen(id string) (publicartifacts.Metadata, *os.File, error) {
	meta, file, err := r.artifacts.Open(strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, publicartifacts.ErrNotFound) {
			return publicartifacts.Metadata{}, nil, toolError("NOT_FOUND", "published artifact not found", "not_found")
		}
		return publicartifacts.Metadata{}, nil, err
	}
	return meta, file, nil
}

func isBlockedPublishPath(absPath, agentDockHome string) bool {
	cleanPath := filepath.Clean(absPath)
	if strings.EqualFold(filepath.Base(cleanPath), "agentdock.env") {
		return true
	}
	home := strings.TrimSpace(agentDockHome)
	if home == "" {
		return false
	}
	realHome := evalOrCleanPath(home)
	if cleanPath == realHome {
		return true
	}
	for _, sub := range []string{"secrets", "oauth"} {
		protectedDir := evalOrCleanPath(filepath.Join(home, sub))
		if isPathWithinDir(cleanPath, protectedDir) {
			return true
		}
	}
	return false
}

func evalOrCleanPath(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(real)
	}
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

func isPathWithinDir(target, dir string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// observeArtifactMutation 把发布物写操作记进 trace（source=internal）。
// 这些方法不经过 Runtime.Call，observeToolCall 的自动覆盖不到；这里按同一套
// 字段补一条记录，并保留 slog 兜底，trace 关闭时审计线索也不丢。
func (r *Runtime) observeArtifactMutation(action, artifactID string, startedAt time.Time, args map[string]any, result Result, callErr error) {
	success := callErr == nil
	errorCode, errorCategory := "", ""
	if !success {
		errorCode, errorCategory = observableError(callErr)
	}
	if r != nil && r.traces != nil {
		r.traces.Record(observability.TraceRecord{
			Tool:          "artifact_" + action,
			Source:        string(observability.SourceInternal),
			StartedAt:     startedAt.UTC(),
			DurationMS:    float64(time.Since(startedAt)) / float64(time.Millisecond),
			Success:       success,
			ErrorCode:     errorCode,
			ErrorCategory: errorCategory,
			Arguments:     tracePayload(args),
			Result:        tracePayload(result),
			Error:         traceErrorText(&callErr),
		})
	}
	slog.Info("artifact mutation", "action", action, "artifact_id", artifactID, "ok", success)
	// 成功的发布物写操作进入事件流；失败不发事件，避免与错误日志重复。
	if success {
		r.publishArtifactEvent(action, artifactID)
	}
}
