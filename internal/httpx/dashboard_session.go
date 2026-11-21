package httpx

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/config"
)

// 会话只属于控制台：它不在 runtime API、/mcp 或 OAuth 里生效。
// 人类会话看到的是代理脱敏后的响应，机器凭证始终留在代理这一跳。
func newDashboardConsoleSession(cfg config.Config) (*auth.ConsoleSession, error) {
	if !cfg.RemoteAuthEnabled {
		return nil, nil
	}
	key, err := auth.LoadOrCreateConsoleSessionKey(filepath.Join(cfg.AgentDockHome, "remote-auth"))
	if err != nil {
		return nil, fmt.Errorf("load console session key: %w", err)
	}
	return auth.NewConsoleSession(key, time.Duration(cfg.RemoteAuthSessionTTLSeconds)*time.Second), nil
}

// dashboardConsoleAuthorized 放行两类请求：本机直连（与启用远程登录前一致），
// 或带有效登录会话的远程请求。远程登录未启用时 console 为 nil，只剩本机直连。
func dashboardConsoleAuthorized(r *http.Request, cfg config.Config, console *auth.ConsoleSession) bool {
	return isDirectLoopbackRequest(r) || dashboardSessionAuthorized(r, cfg, console)
}

func dashboardSessionAuthorized(r *http.Request, cfg config.Config, console *auth.ConsoleSession) bool {
	if !console.Enabled() {
		return false
	}
	cookie, err := r.Cookie(dashboardSessionCookie)
	if err != nil || !console.Valid(cookie.Value, time.Now()) {
		return false
	}
	return dashboardConsoleOriginAllowed(r, cfg)
}

// dashboardConsoleOriginAllowed 校验会话请求的浏览器来源。
// 反向代理可能改写 r.Host（cloudflared 的 quick tunnel 就会），所以同时接受
// “当前 Host”和“配置的公网 Origin 主机名”。DNS rebinding 已经偷不到 host-only
// 的 Cookie，这一层是纵深防御：挡住跨站请求和被篡改的 Origin。
func dashboardConsoleOriginAllowed(r *http.Request, cfg config.Config) bool {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// curl / CLI 客户端不带 Origin；Cookie 仍是唯一凭证。
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	publicURL, err := url.Parse(strings.TrimSpace(cfg.RemoteAuthPublicURL))
	if err != nil || publicURL.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, publicURL.Host)
}

// dashboardSessionDeniesWrite 挡住“会话改写自己的入口”这一类操作：
// 令牌轮换、Nexus 配对、Tunnel 配置与进程重启只允许本机。
// 沿用既有边界（凭据与隧道写入能力与桌面 App 同级，公网访客不可触达），
// 否则一次口令泄漏就能把设备永久交出去。普通运行配置的写入不在其中：
// 它要重启才生效，而重启本身被这一层拦住了。
// 只读状态仍然放行，远程页要能显示在线情况。
func dashboardSessionDeniesWrite(method, target string) bool {
	if method == http.MethodGet || method == http.MethodHead {
		return false
	}
	clean := strings.TrimSuffix(target, "/")
	return clean == "/internal/runtime/config/restart" ||
		clean == "/internal/runtime/remote" ||
		strings.HasPrefix(clean, "/internal/runtime/remote/")
}

func loopbackRuntimeHost(cfg config.Config) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port))
}
