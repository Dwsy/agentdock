package nexusbridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

const identityVersion = 1

const (
	HeaderNexusEdition         = "X-Nexus-Edition"
	HeaderNexusVersion         = "X-Nexus-Version"
	HeaderNexusProtocolVersion = "X-Nexus-Protocol-Version"
	HeaderNexusContractVersion = "X-Nexus-Contract-Version"
	EditionWorkers             = "workers"
	maxServerTokenBytes        = 64
)

// ServerInfo 描述对端 NexusDock 服务端的发行形态与协议版本。
type ServerInfo struct {
	Edition         string `json:"server_edition,omitempty"`
	Version         string `json:"server_version,omitempty"`
	ProtocolVersion string `json:"protocol_version,omitempty"`
	ContractVersion string `json:"contract_version,omitempty"`
}

// IsWorkers 报告对端是否显式标识为 Cloudflare Workers 版 NexusDock。
func (s ServerInfo) IsWorkers() bool {
	return strings.EqualFold(strings.TrimSpace(s.Edition), EditionWorkers)
}

func (s ServerInfo) normalize() ServerInfo {
	return ServerInfo{
		Edition:         normalizeServerToken(s.Edition),
		Version:         normalizeServerToken(s.Version),
		ProtocolVersion: normalizeServerToken(s.ProtocolVersion),
		ContractVersion: normalizeServerToken(s.ContractVersion),
	}
}

type Identity struct {
	Version           int    `json:"version"`
	Endpoint          string `json:"endpoint"`
	WebSocketEndpoint string `json:"websocket_endpoint,omitempty"`
	NodeID            string `json:"node_id"`
	DeviceID          string `json:"device_id"`
	DeviceToken       string `json:"device_token"`
	ServerEdition     string `json:"server_edition,omitempty"`
	ServerVersion     string `json:"server_version,omitempty"`
	ProtocolVersion   string `json:"protocol_version,omitempty"`
	ContractVersion   string `json:"contract_version,omitempty"`
}

// ServerInfo 返回设备身份中持久化记录的服务端版本元数据。
func (i Identity) ServerInfo() ServerInfo {
	return ServerInfo{
		Edition:         i.ServerEdition,
		Version:         i.ServerVersion,
		ProtocolVersion: i.ProtocolVersion,
		ContractVersion: i.ContractVersion,
	}.normalize()
}

// Status 是控制面板可安全展示的配对状态，不暴露 Device Token 本身。
type Status struct {
	Paired            bool   `json:"paired"`
	Endpoint          string `json:"endpoint,omitempty"`
	NodeID            string `json:"node_id,omitempty"`
	DeviceID          string `json:"device_id,omitempty"`
	DeviceTokenStored bool   `json:"device_token_stored"`
	ServerEdition     string `json:"server_edition,omitempty"`
	ServerVersion     string `json:"server_version,omitempty"`
	ProtocolVersion   string `json:"protocol_version,omitempty"`
	ContractVersion   string `json:"contract_version,omitempty"`
	IsWorkers         bool   `json:"is_workers,omitempty"`
}

type PairOptions struct {
	Endpoint string
	Code     string
	Name     string
}

func Pair(ctx context.Context, agentDockHome string, options PairOptions) (Identity, error) {
	endpoint, err := normalizeEndpoint(options.Endpoint)
	if err != nil {
		return Identity{}, err
	}
	code := strings.TrimSpace(options.Code)
	if code == "" {
		return Identity{}, errors.New("NexusDock 配对码不能为空")
	}
	name := strings.TrimSpace(options.Name)
	if name == "" {
		name, err = os.Hostname()
		if err != nil || strings.TrimSpace(name) == "" {
			name = "AgentDock"
		}
	}
	deviceID, err := newDeviceID()
	if err != nil {
		return Identity{}, err
	}
	body, err := json.Marshal(protocol.PairRequest{Code: code, DeviceID: deviceID, Name: name})
	if err != nil {
		return Identity{}, fmt.Errorf("编码 NexusDock 配对请求: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+protocol.PairPath, bytes.NewReader(body))
	if err != nil {
		return Identity{}, fmt.Errorf("创建 NexusDock 配对请求: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return Identity{}, fmt.Errorf("连接 NexusDock 配对接口: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Identity{}, fmt.Errorf("读取 NexusDock 配对响应: %w", err)
	}
	if response.StatusCode != http.StatusCreated {
		return Identity{}, fmt.Errorf("NexusDock 配对失败（HTTP %d）: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	var paired struct {
		protocol.PairResponse
		ServerEdition   string `json:"server_edition"`
		ServerVersion   string `json:"server_version"`
		ProtocolVersion string `json:"protocol_version"`
		ContractVersion string `json:"contract_version"`
	}
	if err := json.Unmarshal(data, &paired); err != nil {
		return Identity{}, fmt.Errorf("解析 NexusDock 配对响应: %w", err)
	}
	if paired.Node.ID == "" || paired.DeviceToken == "" {
		return Identity{}, errors.New("NexusDock 配对响应缺少节点身份")
	}
	webSocketEndpoint := ""
	if strings.TrimSpace(paired.WebSocketEndpoint) != "" {
		webSocketEndpoint, err = normalizeEndpoint(paired.WebSocketEndpoint)
		if err != nil {
			return Identity{}, fmt.Errorf("NexusDock 返回的 WebSocket endpoint 无效: %w", err)
		}
	}
	serverInfo := mergeServerInfo(
		ServerInfo{
			Edition:         paired.ServerEdition,
			Version:         paired.ServerVersion,
			ProtocolVersion: paired.ProtocolVersion,
			ContractVersion: paired.ContractVersion,
		},
		serverInfoFromHeaders(response.Header),
	)
	identity := Identity{
		Version: identityVersion, Endpoint: endpoint, WebSocketEndpoint: webSocketEndpoint,
		NodeID: paired.Node.ID, DeviceID: deviceID, DeviceToken: paired.DeviceToken,
		ServerEdition:   serverInfo.Edition,
		ServerVersion:   serverInfo.Version,
		ProtocolVersion: serverInfo.ProtocolVersion,
		ContractVersion: serverInfo.ContractVersion,
	}
	if err := Save(agentDockHome, identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func Load(agentDockHome string) (Identity, error) {
	data, err := os.ReadFile(identityPath(agentDockHome))
	if errors.Is(err, os.ErrNotExist) {
		return Identity{}, os.ErrNotExist
	}
	if err != nil {
		return Identity{}, fmt.Errorf("读取 NexusDock 设备身份: %w", err)
	}
	var identity Identity
	if err := json.Unmarshal(data, &identity); err != nil {
		return Identity{}, fmt.Errorf("解析 NexusDock 设备身份: %w", err)
	}
	if identity.Version != identityVersion || identity.Endpoint == "" || identity.NodeID == "" || identity.DeviceID == "" || identity.DeviceToken == "" {
		return Identity{}, errors.New("NexusDock 设备身份文件无效")
	}
	if identity.WebSocketEndpoint != "" {
		normalized, err := normalizeEndpoint(identity.WebSocketEndpoint)
		if err != nil {
			return Identity{}, fmt.Errorf("NexusDock 设备身份中的 WebSocket endpoint 无效: %w", err)
		}
		identity.WebSocketEndpoint = normalized
	}
	info := identity.ServerInfo()
	identity.ServerEdition = info.Edition
	identity.ServerVersion = info.Version
	identity.ProtocolVersion = info.ProtocolVersion
	identity.ContractVersion = info.ContractVersion
	return identity, nil
}

func ReadStatus(agentDockHome string) (Status, error) {
	identity, err := Load(agentDockHome)
	if errors.Is(err, os.ErrNotExist) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	info := identity.ServerInfo()
	return Status{
		Paired:            true,
		Endpoint:          identity.Endpoint,
		NodeID:            identity.NodeID,
		DeviceID:          identity.DeviceID,
		DeviceTokenStored: true,
		ServerEdition:     info.Edition,
		ServerVersion:     info.Version,
		ProtocolVersion:   info.ProtocolVersion,
		ContractVersion:   info.ContractVersion,
		IsWorkers:         info.IsWorkers(),
	}, nil
}

func Save(agentDockHome string, identity Identity) error {
	owner, hasOwner := detectInheritedOwner(agentDockHome)
	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 NexusDock 设备身份: %w", err)
	}
	data = append(data, '\n')
	path := identityPath(agentDockHome)
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return fmt.Errorf("保存 NexusDock 设备身份: %w", err)
	}
	if hasOwner {
		if err := applyInheritedOwner(agentDockHome, path, owner); err != nil {
			return fmt.Errorf("同步 NexusDock 设备身份目录属主: %w", err)
		}
	}
	return nil
}

func identityPath(agentDockHome string) string {
	return filepath.Join(agentDockHome, "nexus", "device.json")
}

func normalizeEndpoint(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("NexusDock endpoint 必须是绝对 HTTP(S) 地址")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		host := parsed.Hostname()
		if host != "localhost" && net.ParseIP(host) == nil {
			return "", errors.New("公网 NexusDock endpoint 必须使用 HTTPS")
		}
		if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
			return "", errors.New("公网 NexusDock endpoint 必须使用 HTTPS")
		}
	default:
		return "", errors.New("NexusDock endpoint 必须使用 HTTPS；本机开发可使用 HTTP")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func newDeviceID() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成 AgentDock 设备 ID: %w", err)
	}
	return "device_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func serverInfoFromHeaders(h http.Header) ServerInfo {
	if h == nil {
		return ServerInfo{}
	}
	return ServerInfo{
		Edition:         h.Get(HeaderNexusEdition),
		Version:         h.Get(HeaderNexusVersion),
		ProtocolVersion: h.Get(HeaderNexusProtocolVersion),
		ContractVersion: h.Get(HeaderNexusContractVersion),
	}.normalize()
}

func mergeServerInfo(sources ...ServerInfo) ServerInfo {
	var merged ServerInfo
	for _, raw := range sources {
		s := raw.normalize()
		if merged.Edition == "" {
			merged.Edition = s.Edition
		}
		if merged.Version == "" {
			merged.Version = s.Version
		}
		if merged.ProtocolVersion == "" {
			merged.ProtocolVersion = s.ProtocolVersion
		}
		if merged.ContractVersion == "" {
			merged.ContractVersion = s.ContractVersion
		}
	}
	return merged
}

func normalizeServerToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) <= maxServerTokenBytes {
		return cleaned
	}
	cut := maxServerTokenBytes
	for cut > 0 && !utf8.ValidString(cleaned[:cut]) {
		cut--
	}
	return cleaned[:cut]
}
