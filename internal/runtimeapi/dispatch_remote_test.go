package runtimeapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
)

// remoteRuntimeStub 在 runtimeStub 之上补远程访问管理能力，
// 记录 dispatch 传入的参数，验证路由与请求体解码。
type remoteRuntimeStub struct {
	runtimeStub
	statusCalled bool
	tokenInput   string
	pairEndpoint string
	pairCode     string
	pairName     string
	tunnelMode   string
	tunnelServer string
	tunnelToken  string
}

func (r *remoteRuntimeStub) RuntimeRemoteAccess(context.Context) app.Result {
	r.statusCalled = true
	return app.Result{"ok": true, "source": app.RuntimeAPISource, "auth_token_configured": true}
}

func (r *remoteRuntimeStub) RuntimeRemoteTokenUpdate(token string) (app.Result, error) {
	r.tokenInput = token
	return app.Result{"ok": true, "source": app.RuntimeAPISource, "restart_hint": "重启 Core 后生效"}, nil
}

func (r *remoteRuntimeStub) RuntimeNexusPair(_ context.Context, endpoint, code, name string) (app.Result, error) {
	r.pairEndpoint, r.pairCode, r.pairName = endpoint, code, name
	return app.Result{"ok": true, "source": app.RuntimeAPISource, "paired": true}, nil
}

func (r *remoteRuntimeStub) RuntimeTunnelConfigure(_ context.Context, mode, serverURL, tunnelToken string) (app.Result, error) {
	r.tunnelMode, r.tunnelServer, r.tunnelToken = mode, serverURL, tunnelToken
	return app.Result{"ok": true, "source": app.RuntimeAPISource, "mode": mode}, nil
}

func TestMethodContractForRemoteAccess(t *testing.T) {
	tests := []struct {
		method string
		path   string
		allow  string
		ok     bool
	}{
		{http.MethodGet, "/internal/runtime/remote", "GET", true},
		{http.MethodPost, "/internal/runtime/remote", "GET", false},
		{http.MethodPost, "/internal/runtime/remote/token", "POST", true},
		{http.MethodGet, "/internal/runtime/remote/token", "POST", false},
		{http.MethodPost, "/internal/runtime/remote/nexus/pair", "POST", true},
		{http.MethodGet, "/internal/runtime/remote/nexus/pair", "POST", false},
		{http.MethodPost, "/internal/runtime/remote/tunnel", "POST", true},
		{http.MethodDelete, "/internal/runtime/remote/tunnel", "POST", false},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			if got := MethodAllowed(test.method, test.path); got != test.ok {
				t.Fatalf("MethodAllowed() = %v, want %v", got, test.ok)
			}
			if got := AllowHeader(test.path); got != test.allow {
				t.Fatalf("AllowHeader() = %q, want %q", got, test.allow)
			}
		})
	}
}

func TestDispatchRemoteAccessRoutes(t *testing.T) {
	stub := &remoteRuntimeStub{}

	status, err := Dispatch(context.Background(), stub, Request{
		Method: http.MethodGet,
		Path:   "/internal/runtime/remote",
	})
	if err != nil {
		t.Fatalf("dispatch remote status: %v", err)
	}
	if !stub.statusCalled {
		t.Fatal("expected RuntimeRemoteAccess to be called")
	}
	if ok, _ := status["ok"].(bool); !ok {
		t.Fatalf("unexpected status result: %#v", status)
	}

	tokenBody := `{"token":"plain-probe-token-0123456789abcdef"}`
	if _, err := Dispatch(context.Background(), stub, Request{
		Method: http.MethodPost,
		Path:   "/internal/runtime/remote/token",
		Body:   []byte(tokenBody),
	}); err != nil {
		t.Fatalf("dispatch remote token: %v", err)
	}
	if stub.tokenInput != "plain-probe-token-0123456789abcdef" {
		t.Fatalf("token input = %q", stub.tokenInput)
	}

	pairBody := `{"endpoint":"https://nexus.example.com","code":"abc123","name":"我的设备"}`
	if _, err := Dispatch(context.Background(), stub, Request{
		Method: http.MethodPost,
		Path:   "/internal/runtime/remote/nexus/pair",
		Body:   []byte(pairBody),
	}); err != nil {
		t.Fatalf("dispatch nexus pair: %v", err)
	}
	if stub.pairEndpoint != "https://nexus.example.com" || stub.pairCode != "abc123" || stub.pairName != "我的设备" {
		t.Fatalf("pair inputs = %q %q %q", stub.pairEndpoint, stub.pairCode, stub.pairName)
	}

	tunnelBody := `{"mode":"named","server_url":"https://agent.example.com","tunnel_token":"cf-token"}`
	if _, err := Dispatch(context.Background(), stub, Request{
		Method: http.MethodPost,
		Path:   "/internal/runtime/remote/tunnel",
		Body:   []byte(tunnelBody),
	}); err != nil {
		t.Fatalf("dispatch tunnel configure: %v", err)
	}
	if stub.tunnelMode != "named" || stub.tunnelServer != "https://agent.example.com" || stub.tunnelToken != "cf-token" {
		t.Fatalf("tunnel inputs = %q %q %q", stub.tunnelMode, stub.tunnelServer, stub.tunnelToken)
	}
}

func TestDispatchRemoteAccessUnsupportedRuntime(t *testing.T) {
	stub := &runtimeStub{}
	paths := []string{"/internal/runtime/remote", "/internal/runtime/remote/token", "/internal/runtime/remote/nexus/pair", "/internal/runtime/remote/tunnel"}
	for _, path := range paths {
		method := http.MethodGet
		if strings.Contains(path, "/remote/") {
			method = http.MethodPost
		}
		_, err := Dispatch(context.Background(), stub, Request{Method: method, Path: path, Body: []byte("{}")})
		var toolErr *app.ToolError
		if !asToolError(err, &toolErr) || toolErr.Code != "REMOTE_UNSUPPORTED" {
			t.Fatalf("path %s: error = %#v, want REMOTE_UNSUPPORTED", path, err)
		}
	}
}

func asToolError(err error, target **app.ToolError) bool {
	toolErr, ok := err.(*app.ToolError)
	if ok {
		*target = toolErr
	}
	return ok
}
