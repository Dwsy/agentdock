package httpx

import (
	"bytes"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/runtimeapi"
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
	dashboardBearerRe = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	// Pierre Highlights compiles its bundled lexer with WebAssembly.Module.
	dashboardCSP = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: http: https:; media-src 'self' blob:; frame-src 'self' blob:; object-src 'self' blob:; font-src 'self' data:; connect-src 'self'; base-uri 'self'; frame-ancestors 'none'"
)

// registerDashboard 注册静态页与 API 代理。
// runtimeHandler 是 /internal/runtime/* 的处理器；代理会把 /dashboard/api/*
// 重写成 /internal/runtime/* 并注入 Bearer，避免把 token 暴露给浏览器 JS。
// console 为空表示未启用远程登录，行为与之前完全一致（只允许本机直连）。
func registerDashboard(mux *http.ServeMux, cfg config.Config, console *auth.ConsoleSession, runtimeHandler http.Handler) {
	dist, err := fs.Sub(dashboardFS, "dashboard/dist")
	if err != nil {
		slog.Warn("dashboard: embedded dist unavailable", "error", err)
		return
	}
	if console.Enabled() {
		registerDashboardLogin(mux, newDashboardLogin(cfg, console))
	}
	apiHandler := dashboardAPIHandler(cfg, console, runtimeHandler)

	mux.HandleFunc(dashboardAPIPrefix, apiHandler)
	mux.HandleFunc(dashboardPrefix, func(w http.ResponseWriter, r *http.Request) {
		// 只服务 GET/HEAD，避免静态服务器承载写操作。
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// 启用远程登录后，静态页同样要会话：控制台外壳没有秘密，但未登录就
		// 加载它只会让每个页面都弹一片加载失败，不如直接给登录页。
		if console.Enabled() && !dashboardConsoleAuthorized(r, cfg, console) {
			http.Redirect(w, r, dashboardLoginPath+"?next="+url.QueryEscape(dashboardNextParam(r)), http.StatusSeeOther)
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
// 本机直连直接放行；启用远程登录后，带有效会话 Cookie 的请求也放行，
// 并且只在代理层检查浏览器边界（Host/Origin），到内部 handler 时改写成本机直连形态。
func dashboardAPIHandler(cfg config.Config, console *auth.ConsoleSession, runtimeHandler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		viaSession := false
		if !isDirectLoopbackRequest(r) {
			if !console.Enabled() {
				writeRuntimeAPIError(w, http.StatusForbidden, "LOCAL_ACCESS_REQUIRED", "dashboard API requires direct local access")
				return
			}
			if !dashboardSessionAuthorized(r, cfg, console) {
				writeRuntimeAPIError(w, http.StatusUnauthorized, "SESSION_REQUIRED", "dashboard console login required")
				return
			}
			viaSession = true
		}
		if cfg.AuthToken == "" {
			writeRuntimeAPIError(w, http.StatusServiceUnavailable, "AUTH_TOKEN_REQUIRED", "dashboard requires AGENTDOCK_AUTH_TOKEN")
			return
		}
		sub := strings.TrimPrefix(r.URL.Path, dashboardPrefix) // "api/status"
		// /dashboard/api/status -> /internal/runtime/status
		target := "/internal/runtime/" + strings.TrimPrefix(sub, "api/")
		if viaSession && dashboardSessionDeniesWrite(r.Method, target) {
			writeRuntimeAPIError(w, http.StatusForbidden, "SESSION_SCOPE_DENIED", "console session cannot change remote access or restart the service")
			return
		}

		r2 := r.Clone(r.Context())
		r2.URL.Path = target
		r2.Header = r.Header.Clone()
		r2.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		if viaSession {
			// 浏览器侧校验已完成。内部请求必须呈本机直连形态，否则被反向代理
			// 改写过的 Host 和公开 Origin 会撞上 runtime API 的回环约束，
			// 而它本来就不该为公网宿主放行。
			r2.Host = loopbackRuntimeHost(cfg)
			r2.Header.Del("Origin")
			r2.Header.Del("Referer")
			r2.Header.Del("Cookie")
			for _, header := range []string{"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
				r2.Header.Del(header)
			}
			for _, header := range analyticsProxyHeaders {
				r2.Header.Del(header)
			}
		}
		// SSE 事件流与发布物内容流：直接透传，避免缓冲与 JSON 脱敏改写二进制载荷或破坏 Range。
		if isRuntimeEventsPath(target) || runtimeapi.IsArtifactContentPath(target) {
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
	hasBearer := containsFoldASCIIBytes(in, "bearer")
	if !hasBearer && !bytes.Contains(in, []byte{'\\', 'u'}) && !bytes.Contains(in, []byte{'\\', 'U'}) && !containsAnyQuotedDashboardSensitiveKey(in) && json.Valid(in) {
		return in
	}
	var value any
	if err := json.Unmarshal(in, &value); err != nil {
		if !hasBearer {
			return in
		}
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
			if isDashboardSensitiveKey(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redactDashboardValue(val)
		}
		// 请求头数组项 {"name":"Authorization","value":"..."}：保留名字，洗掉 value。
		if name, ok := out["name"].(string); ok && isDashboardSensitiveKey(name) {
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
		// Shortest possible match for `(?i)Bearer\s+\S{8,}` is 15 bytes ("Bearer " + 8 chars).
		if len(x) >= 15 && containsFoldASCII(x, "bearer") {
			return dashboardBearerRe.ReplaceAllString(x, "Bearer [REDACTED]")
		}
		return x
	default:
		return v
	}
}

func isDashboardSensitiveKey(k string) bool {
	switch len(k) {
	case 5:
		return strings.EqualFold(k, "token")
	case 6:
		return strings.EqualFold(k, "cookie") || strings.EqualFold(k, "secret")
	case 7:
		return strings.EqualFold(k, "api-key")
	case 8:
		return strings.EqualFold(k, "password")
	case 9:
		return strings.EqualFold(k, "x-api-key")
	case 10:
		return strings.EqualFold(k, "set-cookie")
	case 12:
		return strings.EqualFold(k, "access_token")
	case 13:
		return strings.EqualFold(k, "authorization") || strings.EqualFold(k, "refresh_token")
	case 19:
		return strings.EqualFold(k, "proxy-authorization")
	default:
		return false
	}
}

func containsAnyQuotedDashboardSensitiveKey(in []byte) bool {
	for i := 0; i < len(in); {
		rel := bytes.IndexByte(in[i:], '"')
		if rel < 0 {
			return false
		}
		rest := in[i+rel+1:]
		i += rel + 1
		if len(rest) < 6 {
			return false
		}
		switch rest[0] | 0x20 {
		case 't':
			if len(rest) > 5 && rest[5] == '"' && equalFoldASCIIBytes(rest[:5], "token") {
				return true
			}
		case 'c':
			if len(rest) > 6 && rest[6] == '"' && equalFoldASCIIBytes(rest[:6], "cookie") {
				return true
			}
		case 's':
			if len(rest) > 6 && rest[6] == '"' && equalFoldASCIIBytes(rest[:6], "secret") {
				return true
			}
			if len(rest) > 10 && rest[10] == '"' && equalFoldASCIIBytes(rest[:10], "set-cookie") {
				return true
			}
		case 'a':
			if len(rest) > 7 && rest[7] == '"' && equalFoldASCIIBytes(rest[:7], "api-key") {
				return true
			}
			if len(rest) > 12 && rest[12] == '"' && equalFoldASCIIBytes(rest[:12], "access_token") {
				return true
			}
			if len(rest) > 13 && rest[13] == '"' && equalFoldASCIIBytes(rest[:13], "authorization") {
				return true
			}
		case 'p':
			if len(rest) > 8 && rest[8] == '"' && equalFoldASCIIBytes(rest[:8], "password") {
				return true
			}
			if len(rest) > 19 && rest[19] == '"' && equalFoldASCIIBytes(rest[:19], "proxy-authorization") {
				return true
			}
		case 'x':
			if len(rest) > 9 && rest[9] == '"' && equalFoldASCIIBytes(rest[:9], "x-api-key") {
				return true
			}
		case 'r':
			if len(rest) > 13 && rest[13] == '"' && equalFoldASCIIBytes(rest[:13], "refresh_token") {
				return true
			}
		}
	}
	return false
}

func equalFoldASCIIBytes(a []byte, lower string) bool {
	if len(a) != len(lower) {
		return false
	}
	for i := 0; i < len(lower); i++ {
		c := a[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

func containsFoldASCIIBytes(b []byte, lower string) bool {
	n := len(lower)
	if n == 0 {
		return true
	}
	if len(b) < n {
		return false
	}
	limit := len(b) - n
	first := lower[0]
	for i := 0; i <= limit; i++ {
		c := b[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != first {
			continue
		}
		if equalFoldASCIIBytes(b[i:i+n], lower) {
			return true
		}
	}
	return false
}

func containsFoldASCII(s, lower string) bool {
	n := len(lower)
	if n == 0 {
		return true
	}
	if len(s) < n {
		return false
	}
	limit := len(s) - n
	first := lower[0]
	for i := 0; i <= limit; i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != first {
			continue
		}
		matched := true
		for j := 1; j < n; j++ {
			sc := s[i+j]
			if sc >= 'A' && sc <= 'Z' {
				sc += 'a' - 'A'
			}
			if sc != lower[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
