package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/publicartifacts"
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
}
