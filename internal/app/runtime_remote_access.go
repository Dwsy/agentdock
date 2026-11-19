package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/uvwt/agentdock/internal/desktopruntime"
	"github.com/uvwt/agentdock/internal/envstore"
	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

// 远程访问管理（token 轮换、Nexus 配对、Cloudflare Tunnel）走专用端点，
// 不进 editableConfigKeys 白名单：白名单面向常规运行参数，且 init 断言禁止
// 敏感 key 混入；凭据类写入必须是显式的独立契约，各自带自己的校验。
// 信任边界不变：/dashboard/api/* 代理只接受本机直连，隧道流量被 403，
// 因此这里的写入能力与桌面 App 同级，公网访客仍无法触达。

const remoteTokenMinLength = 16
const remoteTokenMaxLength = 512

// nexusStatusOutput 是 `agentdock nexus status --json` 的输出契约镜像
// （nexusbridge.Status）。app 不能直接导入 nexusbridge（它会反向导入 app），
// 与桌面端一样经由 CLI 进程取得；同一可执行文件，无版本漂移风险。
type nexusStatusOutput struct {
	Paired            bool   `json:"paired"`
	Endpoint          string `json:"endpoint"`
	NodeID            string `json:"node_id"`
	DeviceID          string `json:"device_id"`
	DeviceTokenStored bool   `json:"device_token_stored"`
	ServerEdition     string `json:"server_edition"`
	ServerVersion     string `json:"server_version"`
	ProtocolVersion   string `json:"protocol_version"`
	ContractVersion   string `json:"contract_version"`
	IsWorkers         bool   `json:"is_workers"`
}

// RuntimeRemoteAccess 汇总三块远程访问状态。密钥值一律不回显：
// token 只报 configured，Nexus 只报配对元数据，Tunnel 只报模式与地址。
func (r *Runtime) RuntimeRemoteAccess(ctx context.Context) Result {
	root, rootErr := configRuntimeRoot()
	nexus, nexusErr := r.remoteNexusStatus(ctx)
	result := Result{
		"ok":                    true,
		"source":                runtimeAPISource,
		"auth_token_configured": strings.TrimSpace(r.cfg.AuthToken) != "",
		"configurable":          rootErr == nil,
		"nexus":                 nexusMap(nexus, nexusErr),
		"nexus_endpoint_env":    strings.TrimSpace(r.cfg.NexusEndpoint),
		"nexus_device_identity": nexusDeviceIdentityPath(r.cfg.AgentDockHome),
		"agentdock_home":        r.cfg.AgentDockHome,
	}
	tunnel, tunnelErr := desktopruntime.TunnelStatusForRuntime(ctx, root)
	if tunnelErr != nil {
		// 隧道状态失败不应拖垮整个状态页：降级为错误说明，其余照常返回。
		result["tunnel"] = nil
		result["tunnel_error"] = tunnelErr.Error()
	} else {
		result["tunnel"] = Result(tunnelMap(tunnel))
	}
	return result
}

func nexusMap(status nexusStatusOutput, err error) map[string]any {
	if err != nil {
		return map[string]any{"paired": false, "error": err.Error()}
	}
	return map[string]any{
		"paired":              status.Paired,
		"endpoint":            status.Endpoint,
		"node_id":             status.NodeID,
		"device_id":           status.DeviceID,
		"device_token_stored": status.DeviceTokenStored,
		"server_edition":      status.ServerEdition,
		"server_version":      status.ServerVersion,
		"protocol_version":    status.ProtocolVersion,
		"contract_version":    status.ContractVersion,
		"is_workers":          status.IsWorkers,
	}
}

// execNexusStatusCommand 是可替换的执行缝：单测不真的派生子进程
// （os.Executable 在测试里是 test binary，会被当成测试参数重新拉起整个套件）。
var execNexusStatusCommand = func(ctx context.Context, self, home string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, self, "nexus", "status", "--json", "--agentdock-home", home)
	output, err := cmd.Output()
	if err != nil {
		// Output() 只回 stdout；失败原因优先取 ExitError.Stderr。
		detail := err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
				detail = stderr
			}
		}
		return nil, errors.New("读取 Nexus 状态失败: " + detail)
	}
	return output, nil
}

// remoteNexusStatus 经由 CLI 进程读取 Nexus 配对状态，避免 app 与
// nexusbridge 的循环导入；失败时返回错误，由调用方降级展示。
func (r *Runtime) remoteNexusStatus(ctx context.Context) (nexusStatusOutput, error) {
	self, err := os.Executable()
	if err != nil {
		return nexusStatusOutput{}, fmt.Errorf("定位可执行文件失败: %w", err)
	}
	output, err := execNexusStatusCommand(ctx, self, r.cfg.AgentDockHome)
	if err != nil {
		return nexusStatusOutput{}, err
	}
	var status nexusStatusOutput
	if err := json.Unmarshal(output, &status); err != nil {
		return nexusStatusOutput{}, fmt.Errorf("解析 Nexus 状态失败: %w", err)
	}
	return status, nil
}

func tunnelMap(status desktopruntime.TunnelStatus) map[string]any {
	return map[string]any{
		"mode":              status.Mode,
		"running":           status.Running,
		"ready":             status.Ready,
		"startup_enabled":   status.StartupEnabled,
		"public_url":        status.PublicURL,
		"dependency_state":  status.DependencyState,
		"component_version": status.ComponentVersion,
		"connection_state":  status.ConnectionState,
		"recovery_count":    status.RecoveryCount,
	}
}

func nexusDeviceIdentityPath(home string) string {
	return filepath.Join(home, "nexus", "device.json")
}

// validateRemoteAuthToken 只接受无空白、无控制字符的紧凑凭据；
// 拒绝弱值是防止把空串或一两个字符写进 env 后把鉴权悄悄关掉。
func validateRemoteAuthToken(token string) error {
	trimmed := strings.TrimSpace(token)
	if len(trimmed) < remoteTokenMinLength {
		return fmt.Errorf("访问令牌至少 %d 个字符", remoteTokenMinLength)
	}
	if len(trimmed) > remoteTokenMaxLength {
		return fmt.Errorf("访问令牌不能超过 %d 个字符", remoteTokenMaxLength)
	}
	for _, r := range trimmed {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("访问令牌不能包含空白或控制字符")
		}
	}
	return nil
}

// RuntimeRemoteTokenUpdate 轮换 AGENTDOCK_AUTH_TOKEN：原子写 env、保留其它键。
// 新值重启 Core 后才参与鉴权，因此返回值必须带重启提示。
func (r *Runtime) RuntimeRemoteTokenUpdate(token string) (Result, error) {
	if err := validateRemoteAuthToken(token); err != nil {
		return nil, err
	}
	path, err := configEnvFilePath()
	if err != nil {
		return nil, err
	}
	values, err := envstore.ParseFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	if values == nil {
		values = map[string]string{}
	}
	values["AGENTDOCK_AUTH_TOKEN"] = strings.TrimSpace(token)
	if err := atomicfile.Write(path, envstore.Marshal(values), 0o600); err != nil {
		return nil, fmt.Errorf("写入配置失败: %w", err)
	}
	return Result{
		"ok":           true,
		"source":       runtimeAPISource,
		"restart_hint": "访问令牌已保存，重启 Core 后生效。",
	}, nil
}

// RuntimeNexusPair 与桌面端同一条路径：CLI 子进程执行 `agentdock nexus pair`
// （内部即 nexusbridge.Pair），一次性配对码换设备凭据，写入
// AGENTDOCK_HOME/nexus/device.json。与桌面端一致，配对后需重启 Core 建立连接。
func (r *Runtime) RuntimeNexusPair(ctx context.Context, endpoint, code, name string) (Result, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("Nexus 地址不能为空")
	}
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("一次性配对码不能为空")
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("定位可执行文件失败: %w", err)
	}
	args := []string{"nexus", "pair", "--endpoint", strings.TrimSpace(endpoint), "--code", strings.TrimSpace(code), "--agentdock-home", r.cfg.AgentDockHome}
	if deviceName := strings.TrimSpace(name); deviceName != "" {
		args = append(args, "--name", deviceName)
	}
	output, err := exec.CommandContext(ctx, self, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Nexus 配对失败: %w：%s", err, strings.TrimSpace(string(output)))
	}
	// 配对成功后重读状态，把 node_id/endpoint 等元数据带回给前端。
	nexus, _ := r.remoteNexusStatus(ctx)
	return Result{
		"ok":           true,
		"source":       runtimeAPISource,
		"paired":       true,
		"endpoint":     nexus.Endpoint,
		"node_id":      nexus.NodeID,
		"restart_hint": "配对凭据已保存，重启 Core 后建立连接。",
	}, nil
}

// RuntimeTunnelConfigure 通过独立 CLI 进程执行 `agentdock tunnel configure`。
// 不在进程内直接调用平台适配器：configure 可能重启 Tunnel 服务（隧道写操作
// 必须由独立控制进程执行），且 quick/named 首次配置可能需要下载 cloudflared 组件。
// token 经临时文件传递，避免出现在进程参数列表中。
func (r *Runtime) RuntimeTunnelConfigure(ctx context.Context, mode, serverURL, tunnelToken string) (Result, error) {
	root, err := configRuntimeRoot()
	if err != nil {
		return nil, err
	}
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	switch normalizedMode {
	case "none", "quick", "named":
	default:
		return nil, errors.New("tunnel 模式必须是 none、quick 或 named")
	}
	if normalizedMode == "named" && strings.TrimSpace(tunnelToken) == "" {
		return nil, errors.New("named 模式必须提供 Cloudflare Tunnel Token")
	}

	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("定位可执行文件失败: %w", err)
	}
	args := []string{"tunnel", "configure", "--runtime-root", root, "--mode", normalizedMode}
	if server := strings.TrimSpace(serverURL); server != "" {
		args = append(args, "--server-url", server)
	}
	if token := strings.TrimSpace(tunnelToken); token != "" {
		tmp, err := os.CreateTemp(root, ".tunnel-token-*.tmp")
		if err != nil {
			return nil, fmt.Errorf("创建临时 token 文件失败: %w", err)
		}
		tokenPath := tmp.Name()
		if _, err := tmp.WriteString(token + "\n"); err != nil {
			tmp.Close()
			os.Remove(tokenPath)
			return nil, fmt.Errorf("写入临时 token 文件失败: %w", err)
		}
		tmp.Close()
		defer os.Remove(tokenPath)
		args = append(args, "--token-file", tokenPath)
	}

	cmd := exec.CommandContext(ctx, self, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tunnel configure 失败: %w：%s", err, strings.TrimSpace(string(output)))
	}
	hint := "Tunnel 配置已应用。"
	if normalizedMode == "named" {
		hint += "Named Tunnel 已写入 SERVER_URL 与 OAuth 开关，重启 Core 后生效。"
	}
	return Result{
		"ok":           true,
		"source":       runtimeAPISource,
		"mode":         normalizedMode,
		"restart_hint": hint,
	}, nil
}
