package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/config"
)

const (
	testConsolePassword  = "test-console-password"
	testConsolePublicURL = "https://dock.example.com"
	testRemotePeer       = "203.0.113.7:40000"
)

// remoteAuthConfig 是线上 115 那种形态：Core 只听回环，cloudflared 在本机
// 终止 TLS 后以明文转给 Core（r.TLS 为空），所以必须靠 X-Forwarded-Proto 和
// 显式公网地址判断 Secure Cookie。
func remoteAuthConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := testConfig(t)
	cfg.AuthToken = "dashboard-test-token"
	cfg.RemoteAuthEnabled = true
	cfg.RemoteAuthPassword = testConsolePassword
	cfg.RemoteAuthSessionTTLSeconds = 3600
	cfg.RemoteAuthPublicURL = testConsolePublicURL
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateAuth(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newTestConsoleSession(t *testing.T) *auth.ConsoleSession {
	t.Helper()
	key, err := auth.LoadOrCreateConsoleSessionKey(t.TempDir())
	if err != nil {
		t.Fatalf("load console session key: %v", err)
	}
	return auth.NewConsoleSession(key, time.Hour)
}

func newDashboardTestMux(t *testing.T, cfg config.Config, console *auth.ConsoleSession) *http.ServeMux {
	t.Helper()
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	mux := http.NewServeMux()
	registerDashboard(mux, cfg, console, runtimeAPIHandler(runtime, cfg, auth.NewOAuthStore()))
	return mux
}

func newRemoteDashboardRequest(method, target string, body *strings.Reader) *http.Request {
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, "http://dock.example.com"+target, nil)
	} else {
		request = httptest.NewRequest(method, "http://dock.example.com"+target, body)
	}
	request.RemoteAddr = testRemotePeer
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("CF-Connecting-IP", "203.0.113.7")
	return request
}

func serveDashboardRequest(mux *http.ServeMux, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

func loginThroughDashboard(t *testing.T, mux *http.ServeMux, password, next string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"password": {password}}
	if next != "" {
		form.Set("next", next)
	}
	request := newRemoteDashboardRequest(http.MethodPost, dashboardLoginPath, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return serveDashboardRequest(mux, request)
}

func requireSessionCookie(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == dashboardSessionCookie {
			return cookie
		}
	}
	t.Fatalf("no %s cookie in response %d: %s", dashboardSessionCookie, recorder.Code, recorder.Body.String())
	return nil
}

func TestDashboardLoginIssuesSessionCookie(t *testing.T) {
	cfg := remoteAuthConfig(t)
	mux := newDashboardTestMux(t, cfg, newTestConsoleSession(t))

	recorder := loginThroughDashboard(t, mux, testConsolePassword, "/dashboard/tasks")
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303: %s", recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location != "/dashboard/tasks" {
		t.Fatalf("Location = %q, want /dashboard/tasks", location)
	}
	cookie := requireSessionCookie(t, recorder)
	if !cookie.HttpOnly {
		t.Fatal("session cookie is readable from JavaScript")
	}
	if !cookie.Secure {
		t.Fatal("session cookie is not Secure although the proxy terminated TLS")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("SameSite = %v, want Strict", cookie.SameSite)
	}
	if cookie.Path != dashboardSessionCookiePath {
		t.Fatalf("Path = %q, want %q", cookie.Path, dashboardSessionCookiePath)
	}
	if cookie.Domain != "" {
		t.Fatalf("Domain = %q, want host-only cookie", cookie.Domain)
	}
	if cookie.MaxAge <= 0 || cookie.MaxAge > int(cfg.RemoteAuthSessionTTLSeconds) {
		t.Fatalf("MaxAge = %d, want 1..%d", cookie.MaxAge, cfg.RemoteAuthSessionTTLSeconds)
	}
}

func TestDashboardSessionReachesProtectedAPI(t *testing.T) {
	cfg := remoteAuthConfig(t)
	mux := newDashboardTestMux(t, cfg, newTestConsoleSession(t))

	// 未登录：API 明确拒绝，静态页跳到登录页。
	anonymous := newRemoteDashboardRequest(http.MethodGet, "/dashboard/api/status", nil)
	if recorder := serveDashboardRequest(mux, anonymous); recorder.Code != http.StatusUnauthorized ||
		!strings.Contains(recorder.Body.String(), "SESSION_REQUIRED") {
		t.Fatalf("anonymous API status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	anonymousPage := newRemoteDashboardRequest(http.MethodGet, "/dashboard/", nil)
	redirect := serveDashboardRequest(mux, anonymousPage)
	if redirect.Code != http.StatusSeeOther {
		t.Fatalf("anonymous page status = %d, want 303", redirect.Code)
	}
	if location := redirect.Header().Get("Location"); !strings.HasPrefix(location, dashboardLoginPath+"?next=") {
		t.Fatalf("anonymous page Location = %q", location)
	}
	// 静态资源不带返回地址：登录后不该被送到一个 .js 文件里。
	asset := newRemoteDashboardRequest(http.MethodGet, "/dashboard/assets/index.js", nil)
	asset.Header.Set("Sec-Fetch-Dest", "script")
	if location := serveDashboardRequest(mux, asset).Header().Get("Location"); location != dashboardLoginPath+"?next=" {
		t.Fatalf("anonymous asset Location = %q", location)
	}

	cookie := requireSessionCookie(t, loginThroughDashboard(t, mux, testConsolePassword, ""))

	// 登录后：带代理头、公网 Host、非回环对端也能拿到数据。
	statusRequest := newRemoteDashboardRequest(http.MethodGet, "/dashboard/api/status", nil)
	statusRequest.AddCookie(cookie)
	status := serveDashboardRequest(mux, statusRequest)
	if status.Code != http.StatusOK {
		t.Fatalf("session API status = %d body = %s", status.Code, status.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(status.Body.Bytes(), &payload); err != nil {
		t.Fatalf("session API body is not JSON: %v (%s)", err, status.Body.String())
	}
	if payload["ok"] != true {
		t.Fatalf("session API payload = %v", payload)
	}

	// 静态页：登录后正常返回控制台，并保留 CSP。
	pageRequest := newRemoteDashboardRequest(http.MethodGet, "/dashboard/", nil)
	pageRequest.AddCookie(cookie)
	page := serveDashboardRequest(mux, pageRequest)
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("session page status = %d type = %s", page.Code, page.Header().Get("Content-Type"))
	}
	if page.Header().Get("Content-Security-Policy") != dashboardCSP {
		t.Fatalf("session page CSP = %q", page.Header().Get("Content-Security-Policy"))
	}

	// 机器凭证永远不进浏览器。
	if strings.Contains(page.Body.String()+status.Body.String(), cfg.AuthToken) {
		t.Fatal("machine auth token leaked into a console response")
	}
}

func TestDashboardLoginRejectsWrongPassword(t *testing.T) {
	cfg := remoteAuthConfig(t)
	mux := newDashboardTestMux(t, cfg, newTestConsoleSession(t))

	recorder := loginThroughDashboard(t, mux, "wrong-password", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", recorder.Code)
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == dashboardSessionCookie && cookie.Value != "" {
			t.Fatal("failed login issued a session cookie")
		}
	}
	if strings.Contains(recorder.Body.String(), testConsolePassword) {
		t.Fatal("login page echoed the configured password")
	}
}

func TestDashboardLoginRateLimitsAttempts(t *testing.T) {
	cfg := remoteAuthConfig(t)
	mux := newDashboardTestMux(t, cfg, newTestConsoleSession(t))

	for attempt := 1; attempt <= dashboardLoginMaxAttempts; attempt++ {
		if recorder := loginThroughDashboard(t, mux, "wrong-password", ""); recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, recorder.Code)
		}
	}
	limited := loginThroughDashboard(t, mux, testConsolePassword, "")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt beyond the limit status = %d, want 429", limited.Code)
	}
	if limited.Header().Get("Retry-After") == "" {
		t.Fatal("rate limited login has no Retry-After header")
	}
}

func TestDashboardSessionRejectsBadCookieAndOrigin(t *testing.T) {
	cfg := remoteAuthConfig(t)
	console := newTestConsoleSession(t)
	mux := newDashboardTestMux(t, cfg, console)
	cookie := requireSessionCookie(t, loginThroughDashboard(t, mux, testConsolePassword, ""))

	expired, err := console.Issue(time.Now().Add(-2 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	foreignKey, err := auth.LoadOrCreateConsoleSessionKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := auth.NewConsoleSession(foreignKey, time.Hour).Issue(time.Now())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		cookie  *http.Cookie
		headers map[string]string
	}{
		{name: "expired session", cookie: &http.Cookie{Name: dashboardSessionCookie, Value: expired}},
		{name: "foreign key", cookie: &http.Cookie{Name: dashboardSessionCookie, Value: foreign}},
		{name: "tampered", cookie: &http.Cookie{Name: dashboardSessionCookie, Value: cookie.Value + "x"}},
		{name: "cross site", cookie: cookie, headers: map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{name: "foreign origin", cookie: cookie, headers: map[string]string{"Origin": "https://evil.example"}},
		{name: "origin with path", cookie: cookie, headers: map[string]string{"Origin": "https://dock.example.com/dashboard"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := newRemoteDashboardRequest(http.MethodGet, "/dashboard/api/status", nil)
			request.AddCookie(tc.cookie)
			for key, value := range tc.headers {
				request.Header.Set(key, value)
			}
			if recorder := serveDashboardRequest(mux, request); recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", recorder.Code, recorder.Body.String())
			}
		})
	}

	// 与配置的公网 Origin 同名时放行（代理改写了 Host 也认得出来）。
	allowed := newRemoteDashboardRequest(http.MethodGet, "/dashboard/api/status", nil)
	allowed.AddCookie(cookie)
	allowed.Header.Set("Origin", testConsolePublicURL)
	if recorder := serveDashboardRequest(mux, allowed); recorder.Code != http.StatusOK {
		t.Fatalf("public origin status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
}

func TestDashboardSessionCannotManageRemoteAccessOrRestart(t *testing.T) {
	cfg := remoteAuthConfig(t)
	mux := newDashboardTestMux(t, cfg, newTestConsoleSession(t))
	cookie := requireSessionCookie(t, loginThroughDashboard(t, mux, testConsolePassword, ""))

	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/dashboard/api/remote/token"},
		{http.MethodPost, "/dashboard/api/remote/nexus/pair"},
		{http.MethodPost, "/dashboard/api/remote/tunnel"},
		{http.MethodPost, "/dashboard/api/config/restart"},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := newRemoteDashboardRequest(test.method, test.path, strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(cookie)
			recorder := serveDashboardRequest(mux, request)
			if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "SESSION_SCOPE_DENIED") {
				t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}

	// 只读状态仍然可读：远程页需要显示隧道是否在线。
	statusRequest := newRemoteDashboardRequest(http.MethodGet, "/dashboard/api/remote", nil)
	statusRequest.AddCookie(cookie)
	if recorder := serveDashboardRequest(mux, statusRequest); recorder.Code != http.StatusOK {
		t.Fatalf("remote status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestDashboardLoopbackAccessSurvivesRemoteLogin(t *testing.T) {
	cfg := remoteAuthConfig(t)
	mux := newDashboardTestMux(t, cfg, newTestConsoleSession(t))

	status := serveDashboardRequest(mux, newLocalRuntimeRequest(http.MethodGet, "http://127.0.0.1:8765/dashboard/api/status", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("local API status = %d body = %s", status.Code, status.Body.String())
	}
	page := serveDashboardRequest(mux, newLocalRuntimeRequest(http.MethodGet, "http://127.0.0.1:8765/dashboard/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("local page status = %d, want 200", page.Code)
	}
}

func TestDashboardSessionLoginDisabledKeepsLoopbackBoundary(t *testing.T) {
	cfg := testConfig(t)
	cfg.AuthToken = "dashboard-test-token"
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	mux := newDashboardTestMux(t, cfg, nil)

	request := newRemoteDashboardRequest(http.MethodGet, "/dashboard/api/status", nil)
	recorder := serveDashboardRequest(mux, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "LOCAL_ACCESS_REQUIRED") {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	// 未启用远程登录时静态页行为不变：不回跳登录页，直接给控制台外壳。
	page := serveDashboardRequest(mux, newRemoteDashboardRequest(http.MethodGet, "/dashboard/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", page.Code)
	}
}

func TestDashboardLoginNextCannotEscapeConsole(t *testing.T) {
	cfg := remoteAuthConfig(t)
	mux := newDashboardTestMux(t, cfg, newTestConsoleSession(t))

	for _, next := range []string{
		"https://evil.example/steal",
		"//evil.example/steal",
		"/administrator",
		"/dashboard/login",
		"/dashboard/../etc/passwd",
	} {
		t.Run(next, func(t *testing.T) {
			recorder := loginThroughDashboard(t, mux, testConsolePassword, next)
			if recorder.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", recorder.Code)
			}
			if location := recorder.Header().Get("Location"); location != dashboardPrefix {
				t.Fatalf("Location = %q, want %q", location, dashboardPrefix)
			}
		})
	}
}

// 跨站导航带不上 Strict Cookie，但如果某个客户端硬塞了有效 Cookie，登录页
// 不能再跳回 /dashboard/（那边会因跨站拦截再打回来），只能老老实实给表单。
func TestDashboardLoginPageDoesNotBounceCrossSiteSession(t *testing.T) {
	cfg := remoteAuthConfig(t)
	mux := newDashboardTestMux(t, cfg, newTestConsoleSession(t))
	cookie := requireSessionCookie(t, loginThroughDashboard(t, mux, testConsolePassword, ""))

	request := newRemoteDashboardRequest(http.MethodGet, dashboardLoginPath+"?next=/dashboard/tasks", nil)
	request.AddCookie(cookie)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	recorder := serveDashboardRequest(mux, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (form, not a redirect)", recorder.Code)
	}

	// 同源导航仍然直接回控制台。
	sameSite := newRemoteDashboardRequest(http.MethodGet, dashboardLoginPath+"?next=/dashboard/tasks", nil)
	sameSite.AddCookie(cookie)
	sameSite.Header.Set("Sec-Fetch-Site", "same-origin")
	redirect := serveDashboardRequest(mux, sameSite)
	if redirect.Code != http.StatusSeeOther || redirect.Header().Get("Location") != "/dashboard/tasks" {
		t.Fatalf("status = %d location = %q", redirect.Code, redirect.Header().Get("Location"))
	}
}

func TestDashboardLoginPageIsSelfContained(t *testing.T) {
	cfg := remoteAuthConfig(t)
	mux := newDashboardTestMux(t, cfg, newTestConsoleSession(t))

	recorder := serveDashboardRequest(mux, newRemoteDashboardRequest(http.MethodGet, dashboardLoginPath, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("login page status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `action="/dashboard/login"`) {
		t.Fatalf("login page has no form action: %s", body)
	}
	for _, forbidden := range []string{"<script", "http://", "https://"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("login page contains %q, so it is not self-contained", forbidden)
		}
	}
	if csp := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("login page CSP = %q", csp)
	}
	if cookie := recorder.Header().Get("Set-Cookie"); cookie != "" {
		t.Fatalf("login page set a cookie: %s", cookie)
	}

	// 登录路由优先于 SPA 回退：不能返回控制台 index.html。
	if strings.Contains(body, "solid-glass") {
		t.Fatal("login route fell through to the SPA shell")
	}
}

func TestNewDashboardConsoleSessionHonoursEnableFlag(t *testing.T) {
	cfg := remoteAuthConfig(t)
	console, err := newDashboardConsoleSession(cfg)
	if err != nil {
		t.Fatalf("newDashboardConsoleSession() error = %v", err)
	}
	if !console.Enabled() {
		t.Fatal("console session disabled although remote auth is enabled")
	}
	if ttl := console.TTL(); ttl != time.Hour {
		t.Fatalf("console TTL = %v, want 1h", ttl)
	}

	disabled := remoteAuthConfig(t)
	disabled.RemoteAuthEnabled = false
	console, err = newDashboardConsoleSession(disabled)
	if err != nil {
		t.Fatalf("disabled newDashboardConsoleSession() error = %v", err)
	}
	if console.Enabled() {
		t.Fatal("console session enabled although remote auth is disabled")
	}
}
