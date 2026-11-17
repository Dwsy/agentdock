package httpx

import (
	"bytes"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/config"
)

// embed 文件无修改时间；ServeContent 用零值即可（禁用 304 缓存）。
var zeroTime time.Time

// Dashboard 是随 Core 分发的只读 Web 控制台。构建产物放在 dist/ 下由
// go:embed 打进 Core 二进制，托盘菜单通过 http://127.0.0.1:<port>/dashboard 打开。
// dist/ 的构建产物随仓库提交，因此任何 go build 都自带完整控制台；
// 前端改动后用 packaging/macos/build-dashboard.sh 刷新它。
//
//go:embed dashboard/dist
var dashboardFS embed.FS

const (
	dashboardPrefix    = "/dashboard/"
	dashboardAPIPrefix = "/dashboard/api/"
)

var (
	dashboardBearerRe    = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	dashboardSensitiveRe = regexp.MustCompile(`(?i)^(authorization|cookie|set-cookie|x-api-key|api-key|proxy-authorization|access_token|refresh_token|token|password|secret)$`)
	dashboardCSP         = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; base-uri 'self'; frame-ancestors 'none'"
)

// registerDashboard 注册静态页与 API 代理。
// runtimeHandler 是 /internal/runtime/* 的处理器；代理会把 /dashboard/api/*
// 重写成 /internal/runtime/* 并注入 Bearer，避免把 token 暴露给浏览器 JS。
func registerDashboard(mux *http.ServeMux, cfg config.Config, runtimeHandler http.Handler) {
	dist, err := fs.Sub(dashboardFS, "dashboard/dist")
	if err != nil {
		slog.Warn("dashboard: embedded dist unavailable", "error", err)
		return
	}
	apiHandler := dashboardAPIHandler(cfg, runtimeHandler)

	mux.HandleFunc(dashboardAPIPrefix, apiHandler)
	mux.HandleFunc(dashboardPrefix, func(w http.ResponseWriter, r *http.Request) {
		// 只服务 GET/HEAD，避免静态服务器承载写操作。
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Security-Policy", dashboardCSP)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// SPA 回退：非静态资源请求统一落到 index.html。
		path := strings.TrimPrefix(r.URL.Path, dashboardPrefix)
		name := "index.html"
		if path != "" {
			if info, statErr := fs.Stat(dist, path); statErr == nil && !info.IsDir() {
				name = path
			}
		}
		serveDashboardFile(w, r, dist, name)
	})
}

// serveDashboardFile 直接从 embed FS 写出文件。刻意不用 http.FileServer：
// 后者会把 /index.html 301 到 ./，在 /dashboard/ 前缀下产生跳转循环。
func serveDashboardFile(w http.ResponseWriter, r *http.Request, dist fs.FS, name string) {
	data, err := fs.ReadFile(dist, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, name, zeroTime, bytes.NewReader(data))
}

// dashboardAPIHandler 把 /dashboard/api/* 代理到 /internal/runtime/*。
// 仅接受本机直连（无跨域代理头），注入 Core 的 AuthToken 后交给 runtime handler。
func dashboardAPIHandler(cfg config.Config, runtimeHandler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isDirectLoopbackRequest(r) {
			writeRuntimeAPIError(w, http.StatusForbidden, "LOCAL_ACCESS_REQUIRED", "dashboard API requires direct local access")
			return
		}
		if cfg.AuthToken == "" {
			writeRuntimeAPIError(w, http.StatusServiceUnavailable, "AUTH_TOKEN_REQUIRED", "dashboard requires AGENTDOCK_AUTH_TOKEN")
			return
		}
		sub := strings.TrimPrefix(r.URL.Path, dashboardPrefix) // "api/status"
		// /dashboard/api/status -> /internal/runtime/status
		target := "/internal/runtime/" + strings.TrimPrefix(sub, "api/")

		r2 := r.Clone(r.Context())
		r2.URL.Path = target
		r2.Header = r.Header.Clone()
		r2.Header.Set("Authorization", "Bearer "+cfg.AuthToken)

		// SSE 事件流：直接透传，避免缓冲（事件 payload 是摘要投影，无凭据）。
		if isRuntimeEventsPath(target) {
			runtimeHandler.ServeHTTP(w, r2)
			return
		}

		// 普通 JSON：先捕获、脱敏，再写出，避免把 Authorization 等原文送到浏览器。
		rec := &dashboardRecorder{header: make(http.Header), status: http.StatusOK}
		runtimeHandler.ServeHTTP(rec, r2)
		body := sanitizeDashboardJSON(rec.body.Bytes())
		for k, vals := range rec.header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.status)
		_, _ = w.Write(body)
	}
}

// dashboardRecorder 缓冲非 SSE 响应，便于脱敏后再写出。
type dashboardRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (d *dashboardRecorder) Header() http.Header  { return d.header }
func (d *dashboardRecorder) WriteHeader(code int) { d.status = code }
func (d *dashboardRecorder) Write(p []byte) (int, error) {
	return d.body.Write(p)
}

// sanitizeDashboardJSON 递归脱敏敏感字段，并对残留 Bearer 串做兜底清理。
func sanitizeDashboardJSON(in []byte) []byte {
	var value any
	if err := json.Unmarshal(in, &value); err != nil {
		return dashboardBearerRe.ReplaceAll(in, []byte("Bearer [REDACTED]"))
	}
	clean := redactDashboardValue(value)
	out, err := json.Marshal(clean)
	if err != nil {
		return in
	}
	return out
}

func redactDashboardValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if dashboardSensitiveRe.MatchString(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redactDashboardValue(val)
		}
		// 请求头数组项 {"name":"Authorization","value":"..."}：保留名字，洗掉 value。
		if name, ok := out["name"].(string); ok && dashboardSensitiveRe.MatchString(name) {
			if _, hasValue := out["value"]; hasValue {
				out["value"] = "[REDACTED]"
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = redactDashboardValue(item)
		}
		return out
	case string:
		return dashboardBearerRe.ReplaceAllString(x, "Bearer [REDACTED]")
	default:
		return v
	}
}
