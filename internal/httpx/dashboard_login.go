package httpx

import (
	"bytes"
	_ "embed"
	"html/template"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/config"
)

// 登录页必须自包含：控制台在登录前不许加载任何外部资源，
// 否则一次远程暴露就同时依赖第三方 CDN 与浏览器缓存策略。
//
//go:embed dashboard_login_page.html
var dashboardLoginPageHTML string

var dashboardLoginPageTemplate = template.Must(template.New("dashboard-login").Parse(dashboardLoginPageHTML))

const (
	dashboardLoginPath  = "/dashboard/login"
	dashboardLogoutPath = "/dashboard/logout"
	// 会话只发给控制台路径，别的端点拿不到它；host-only（不设 Domain），
	// 因此 DNS rebinding 也偷不到。
	dashboardSessionCookie     = "agentdock_session"
	dashboardSessionCookiePath = "/dashboard"
	dashboardLoginBodyLimit    = 4 << 10
	dashboardLoginMaxAttempts  = 10
	dashboardLoginWindow       = 5 * time.Minute
)

// 登录页无脚本、无外部资源；表单只能回本服务。
const dashboardLoginCSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

type dashboardLoginPageData struct {
	Error string
	Next  string
}

// dashboardLogin 处理口令换取会话 Cookie 的两条路由。
// 口令来自 AGENTDOCK_REMOTE_AUTH_PASSWORD，只与常量时间比较，永不回显。
type dashboardLogin struct {
	cfg     config.Config
	console *auth.ConsoleSession
	limiter *fixedWindowLimiter
}

func newDashboardLogin(cfg config.Config, console *auth.ConsoleSession) *dashboardLogin {
	return &dashboardLogin{
		cfg:     cfg,
		console: console,
		limiter: newFixedWindowLimiter(dashboardLoginMaxAttempts, dashboardLoginWindow),
	}
}

func registerDashboardLogin(mux *http.ServeMux, login *dashboardLogin) {
	mux.HandleFunc(dashboardLoginPath, login.handle)
	mux.HandleFunc(dashboardLogoutPath, login.handleLogout)
}

func (l *dashboardLogin) handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		next := dashboardSafeNext(r.URL.Query().Get("next"))
		// 已登录用户不该再看到登录表单（返回键回到这里时尤其明显）。
		// 跨站导航不顺带跳转：如果某个浏览器仍会带上 Strict Cookie，直跳会被
		// 静态页的跨站拦截再打回本页，变成来回跳转的死循环。
		if cookie, err := r.Cookie(dashboardSessionCookie); err == nil &&
			l.console.Valid(cookie.Value, time.Now()) && dashboardConsoleOriginAllowed(r, l.cfg) {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		l.render(w, r, http.StatusOK, "", next)
	case http.MethodPost:
		l.authenticate(w, r)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (l *dashboardLogin) authenticate(w http.ResponseWriter, r *http.Request) {
	if !l.limiter.Allow(requestRemoteIP(r, l.cfg), time.Now()) {
		w.Header().Set("Retry-After", "300")
		l.render(w, r, http.StatusTooManyRequests, "尝试次数过多，请稍后再试。", dashboardPrefix)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/x-www-form-urlencoded") {
		l.render(w, r, http.StatusBadRequest, "登录请求格式不正确。", dashboardPrefix)
		return
	}
	// 限速之后才开始读请求体，并立刻套上大小上限：登录表单只有两个字段。
	r.Body = http.MaxBytesReader(w, r.Body, dashboardLoginBodyLimit)
	if err := r.ParseForm(); err != nil {
		l.render(w, r, http.StatusBadRequest, "登录请求无法解析。", dashboardPrefix)
		return
	}
	next := dashboardSafeNext(r.PostFormValue("next"))
	if !auth.ConstantTimeEqual(r.PostFormValue("password"), l.cfg.RemoteAuthPassword) {
		// 只记来源 IP，不记口令，也不区分“口令错误”与其他原因。
		slog.Warn("dashboard login rejected", "remote", requestRemoteIP(r, l.cfg))
		l.render(w, r, http.StatusUnauthorized, "访问口令不正确。", next)
		return
	}
	token, err := l.console.Issue(time.Now())
	if err != nil {
		slog.Error("dashboard login failed to issue session", "error", err)
		l.render(w, r, http.StatusInternalServerError, "无法创建登录会话。", dashboardPrefix)
		return
	}
	http.SetCookie(w, dashboardSessionCookieFor(r, l.cfg, token, l.console.TTL()))
	slog.Info("dashboard login succeeded", "remote", requestRemoteIP(r, l.cfg))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// handleLogout 清掉 Cookie 后回到登录页。GET 也接受，方便直接收藏一个退出链接；
// 被第三方页面诱导退出只是骚扰，不构成越权。
func (l *dashboardLogin) handleLogout(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost:
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.SetCookie(w, dashboardSessionCookieFor(r, l.cfg, "", 0))
	http.Redirect(w, r, dashboardLoginPath, http.StatusSeeOther)
}

func (l *dashboardLogin) render(w http.ResponseWriter, r *http.Request, status int, message, next string) {
	var body bytes.Buffer
	if err := dashboardLoginPageTemplate.Execute(&body, dashboardLoginPageData{Error: message, Next: next}); err != nil {
		slog.Error("dashboard login page render failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", dashboardLoginCSP)
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

// dashboardNextParam 只在浏览器导航时带上返回地址。静态资源被匿名请求时
// 不带 next，否则登录完会被送到一个 .js 文件里。
func dashboardNextParam(r *http.Request) string {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return ""
	}
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "document", "iframe", "empty", "":
		return r.URL.RequestURI()
	default:
		return ""
	}
}

// dashboardSafeNext 只接受控制台内部的相对路径，避免登录后被重定向到外部站点。
func dashboardSafeNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, dashboardPrefix) || strings.HasPrefix(raw, "//") {
		return dashboardPrefix
	}
	if strings.HasPrefix(raw, dashboardLoginPath) || strings.HasPrefix(raw, dashboardLogoutPath) || strings.ContainsAny(raw, "\\\r\n\t") {
		return dashboardPrefix
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil {
		return dashboardPrefix
	}
	// http.Redirect 会清理路径：/dashboard/../x 会变成 /x，那就离开控制台了。
	cleanPath := strings.TrimSuffix(parsed.Path, "/")
	if path.Clean(cleanPath) != cleanPath {
		return dashboardPrefix
	}
	return raw
}

// dashboardSessionCookieFor 构造会话 Cookie。value 为空时返回删除指令（MaxAge<0）。
func dashboardSessionCookieFor(r *http.Request, cfg config.Config, value string, ttl time.Duration) *http.Cookie {
	cookie := &http.Cookie{
		Name:     dashboardSessionCookie,
		Value:    value,
		Path:     dashboardSessionCookiePath,
		Secure:   dashboardCookieSecure(r, cfg),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
	if value == "" {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(0, 0).UTC()
		return cookie
	}
	cookie.MaxAge = int(ttl / time.Second)
	cookie.Expires = time.Now().Add(ttl).UTC()
	return cookie
}

// TLS 通常在反向代理处终止，Core 只看到明文 HTTP；此时必须靠
// X-Forwarded-Proto 或显式公网地址判断，否则 Secure Cookie 永远下发不出去。
func dashboardCookieSecure(r *http.Request, cfg config.Config) bool {
	if r.TLS != nil || strings.EqualFold(firstHeaderToken(r.Header.Get("X-Forwarded-Proto")), "https") {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.RemoteAuthPublicURL)), "https://")
}
