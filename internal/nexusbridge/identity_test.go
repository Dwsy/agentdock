package nexusbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPairPersistsOnlyNexusDeviceIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/nodes/pair" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["code"] != "pair_test" || request["device_id"] == "" {
			t.Fatalf("request = %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"node":{"id":"node_test"},"device_token":"device-secret"}`))
	}))
	defer server.Close()

	home := t.TempDir()
	identity, err := Pair(t.Context(), home, PairOptions{Endpoint: server.URL, Code: "pair_test", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.NodeID != "node_test" || identity.DeviceToken != "device-secret" {
		t.Fatalf("identity = %#v", identity)
	}
	loaded, err := Load(home)
	if err != nil || loaded != identity {
		t.Fatalf("loaded = %#v err=%v", loaded, err)
	}
	info, err := os.Stat(filepath.Join(home, "nexus", "device.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions = %o", info.Mode().Perm())
	}
	status, err := ReadStatus(home)
	if err != nil || !status.Paired || !status.DeviceTokenStored || status.Endpoint != server.URL || status.NodeID != "node_test" {
		t.Fatalf("status = %#v err=%v", status, err)
	}
}

func TestReadStatusDoesNotExposeDeviceToken(t *testing.T) {
	status, err := ReadStatus(t.TempDir())
	if err != nil || status.Paired || status.DeviceTokenStored {
		t.Fatalf("status = %#v err=%v", status, err)
	}
	data, err := json.Marshal(Status{Paired: true, DeviceTokenStored: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "device_token\"") {
		t.Fatalf("status JSON exposed Device Token field: %s", data)
	}
}

func TestPublicEndpointRequiresHTTPS(t *testing.T) {
	if _, err := normalizeEndpoint("http://nexus.example.com"); err == nil {
		t.Fatal("public HTTP endpoint was accepted")
	}
	if endpoint, err := normalizeEndpoint("http://127.0.0.1:8080/"); err != nil || endpoint != "http://127.0.0.1:8080" {
		t.Fatalf("loopback endpoint=%q err=%v", endpoint, err)
	}
}

func TestPairPersistsOptionalWebSocketEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"node":{"id":"node_ws"},"device_token":"device-secret","websocket_endpoint":"http://127.0.0.1:8765/"}`))
	}))
	defer server.Close()

	home := t.TempDir()
	identity, err := Pair(t.Context(), home, PairOptions{Endpoint: server.URL, Code: "pair_test", Name: "DockMini"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.WebSocketEndpoint != "http://127.0.0.1:8765" {
		t.Fatalf("websocket endpoint = %q", identity.WebSocketEndpoint)
	}
	loaded, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WebSocketEndpoint != identity.WebSocketEndpoint {
		t.Fatalf("loaded websocket endpoint = %q, want %q", loaded.WebSocketEndpoint, identity.WebSocketEndpoint)
	}
}

func TestPairRejectsInvalidWebSocketEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"node":{"id":"node_ws"},"device_token":"device-secret","websocket_endpoint":"http://public.example.com"}`))
	}))
	defer server.Close()

	_, err := Pair(t.Context(), t.TempDir(), PairOptions{Endpoint: server.URL, Code: "pair_test", Name: "DockMini"})
	if err == nil || !strings.Contains(err.Error(), "WebSocket endpoint") {
		t.Fatalf("invalid websocket endpoint should be rejected, err=%v", err)
	}
}

func TestLoadLegacyIdentityWithoutWebSocketEndpoint(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "nexus", "device.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"version":1,"endpoint":"https://nexus.example.com","node_id":"node_legacy","device_id":"device_legacy_01","device_token":"secret"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	identity, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if identity.WebSocketEndpoint != "" || identity.Endpoint != "https://nexus.example.com" {
		t.Fatalf("legacy identity = %#v", identity)
	}
}

func TestPairPersistsWorkersEditionAndProtocolVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(HeaderNexusEdition, EditionWorkers)
		w.Header().Set(HeaderNexusVersion, "0.1.0-workers")
		w.Header().Set(HeaderNexusProtocolVersion, "2")
		w.Header().Set(HeaderNexusContractVersion, "0.9.0")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"node":{"id":"node_workers"},
			"device_token":"nx_workers_secret",
			"server_edition":"workers",
			"server_version":"0.1.0-workers",
			"protocol_version":"2",
			"contract_version":"0.9.0"
		}`))
	}))
	defer server.Close()

	home := t.TempDir()
	identity, err := Pair(t.Context(), home, PairOptions{Endpoint: server.URL, Code: "pair_workers", Name: "WorkersNode"})
	if err != nil {
		t.Fatal(err)
	}
	if !identity.ServerInfo().IsWorkers() || identity.ProtocolVersion != "2" || identity.ContractVersion != "0.9.0" || identity.ServerVersion != "0.1.0-workers" {
		t.Fatalf("identity server info = %#v", identity)
	}

	status, err := ReadStatus(home)
	if err != nil {
		t.Fatal(err)
	}
	if !status.IsWorkers || status.ServerEdition != EditionWorkers || status.ProtocolVersion != "2" || status.ContractVersion != "0.9.0" {
		t.Fatalf("status = %#v", status)
	}

	// 即使响应体不带版本字段，仅靠 HTTP 响应头也能识别 Workers 版本与协议号。
	headerOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(HeaderNexusEdition, "workers")
		w.Header().Set(HeaderNexusProtocolVersion, "2")
		w.Header().Set(HeaderNexusContractVersion, "0.9.0")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"node":{"id":"node_hdr"},"device_token":"nx_hdr_secret"}`))
	}))
	defer headerOnly.Close()

	hdrHome := t.TempDir()
	hdrIdentity, err := Pair(t.Context(), hdrHome, PairOptions{Endpoint: headerOnly.URL, Code: "pair_hdr", Name: "HeaderNode"})
	if err != nil {
		t.Fatal(err)
	}
	if !hdrIdentity.ServerInfo().IsWorkers() || hdrIdentity.ProtocolVersion != "2" || hdrIdentity.ContractVersion != "0.9.0" {
		t.Fatalf("header fallback identity = %#v", hdrIdentity)
	}
}
