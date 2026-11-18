package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/desktopruntime"
	"github.com/uvwt/agentdock/internal/envstore"
	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/manualllm"
)

// 配置 API 只暴露这份白名单里的键：token、OAuth 密钥、设备身份等敏感项
// 既不回显也不允许从这里改，避免把凭据带进 Web 控制台。
var editableConfigKeys = []string{
	"AGENTDOCK_HOST",
	"AGENTDOCK_PORT",
	"AGENTDOCK_LOG_LEVEL",
	"AGENTDOCK_MCP_APPS_MODE",
	"AGENTDOCK_BROWSER_ENABLED",
	"AGENTDOCK_BROWSER_CDP_URL",
	"AGENTDOCK_BROWSER_REUSE_EXISTING_CDP",
	"AGENTDOCK_ACP_ENABLED",
	"AGENTDOCK_ACP_DEFAULT_PROFILE",
	"AGENTDOCK_MANUAL_LLM_ENABLED",
	"AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES",
	"AGENTDOCK_MANUAL_LLM_SLICE_BYTES",
	"AGENTDOCK_MANUAL_LLM_MAX_SLICE_BYTES",
	"AGENTDOCK_MANUAL_LLM_WAIT_MS",
	"AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS",
	"AGENTDOCK_MANUAL_LLM_MAX_PENDING",
	"AGENTDOCK_MANUAL_LLM_CACHE_MAX_RESPONSES",
	"AGENTDOCK_MANUAL_LLM_CACHE_TTL_SEC",
	"AGENTDOCK_MANUAL_LLM_KEEPALIVE_MS",
	"AGENTDOCK_MANUAL_LLM_CACHE_MAX_IMAGE_MB",
	"AGENTDOCK_TRACE_MODE",
	"AGENTDOCK_TRACE_RETENTION_DAYS",
	"AGENTDOCK_NETWORK_CAPTURE_ENABLED",
	"AGENTDOCK_NETWORK_CAPTURE_MAX",
}

func init() {
	// 启动期一次性校验白名单，防止手滑把敏感 key 加进来。
	for _, key := range editableConfigKeys {
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") {
			panic("editableConfigKeys contains sensitive key: " + key)
		}
	}
}

var errConfigUnsupported = errors.New("配置管理需要桌面运行目录（AGENTDOCK_RUNTIME_ROOT）")

func configRuntimeRoot() (string, error) {
	root := strings.TrimSpace(os.Getenv("AGENTDOCK_RUNTIME_ROOT"))
	if root == "" {
		root = strings.TrimSpace(desktopruntime.DefaultRuntimeRoot())
	}
	if root == "" {
		return "", errConfigUnsupported
	}
	return root, nil
}

func configEnvFilePath() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("AGENTDOCK_ENV_FILE")); explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("解析 AGENTDOCK_ENV_FILE 失败: %w", err)
		}
		return abs, nil
	}
	root, err := configRuntimeRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "agentdock.env"), nil
}

// RuntimeConfig 回显当前生效的可编辑配置（来自进程环境，非 env 文件），值均已脱敏。
func (r *Runtime) RuntimeConfig() Result {
	cfg := r.cfg
	values := map[string]string{
		"AGENTDOCK_HOST":                           cfg.Host,
		"AGENTDOCK_PORT":                           strconv.Itoa(cfg.Port),
		"AGENTDOCK_LOG_LEVEL":                      cfg.LogLevel,
		"AGENTDOCK_MCP_APPS_MODE":                  string(cfg.MCPAppsMode),
		"AGENTDOCK_BROWSER_ENABLED":                strconv.FormatBool(cfg.BrowserEnabled),
		"AGENTDOCK_BROWSER_CDP_URL":                cfg.BrowserCDPURL,
		"AGENTDOCK_BROWSER_REUSE_EXISTING_CDP":     strconv.FormatBool(cfg.BrowserReuseExistingCDP),
		"AGENTDOCK_ACP_ENABLED":                    strconv.FormatBool(cfg.ACPEnabled),
		"AGENTDOCK_ACP_DEFAULT_PROFILE":            cfg.ACPDefaultProfile,
		"AGENTDOCK_MANUAL_LLM_ENABLED":             strconv.FormatBool(r.ManualLLMEnabled()),
		"AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES":       strconv.Itoa(cfg.ManualLLMPreviewBytes),
		"AGENTDOCK_MANUAL_LLM_SLICE_BYTES":         strconv.Itoa(cfg.ManualLLMSliceBytes),
		"AGENTDOCK_MANUAL_LLM_MAX_SLICE_BYTES":     strconv.Itoa(cfg.ManualLLMMaxSliceBytes),
		"AGENTDOCK_MANUAL_LLM_WAIT_MS":             strconv.Itoa(cfg.ManualLLMWaitMs),
		"AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS":         strconv.Itoa(cfg.ManualLLMMaxWaitMs),
		"AGENTDOCK_MANUAL_LLM_MAX_PENDING":         strconv.Itoa(cfg.ManualLLMMaxPending),
		"AGENTDOCK_MANUAL_LLM_CACHE_MAX_RESPONSES": strconv.Itoa(cfg.ManualLLMCacheMaxResponses),
		"AGENTDOCK_MANUAL_LLM_CACHE_TTL_SEC":       strconv.Itoa(cfg.ManualLLMCacheTTLSec),
		"AGENTDOCK_MANUAL_LLM_KEEPALIVE_MS":        strconv.Itoa(cfg.ManualLLMKeepaliveMS),
		"AGENTDOCK_MANUAL_LLM_CACHE_MAX_IMAGE_MB":  strconv.Itoa(cfg.ManualLLMCacheMaxImageMB),
		"AGENTDOCK_TRACE_MODE":                     string(cfg.TraceMode),
		"AGENTDOCK_TRACE_RETENTION_DAYS":           strconv.Itoa(cfg.TraceRetentionDays),
		"AGENTDOCK_NETWORK_CAPTURE_ENABLED":        strconv.FormatBool(cfg.NetworkCaptureEnabled),
		"AGENTDOCK_NETWORK_CAPTURE_MAX":            strconv.Itoa(cfg.NetworkCaptureMax),
	}
	_, rootErr := configRuntimeRoot()
	configFile, fileErr := configEnvFilePath()
	return Result{
		"ok":          true,
		"source":      runtimeAPISource,
		"values":      values,
		"keys":        editableConfigKeys,
		"restartable": rootErr == nil,
		"config_file": func() string {
			if fileErr == nil {
				return configFile
			}
			return ""
		}(),
		"restart_hint": "配置保存在 agentdock.env，重启 Core 后生效。",
	}
}

func validateConfigUpdate(key, value string) error {
	switch key {
	case "AGENTDOCK_HOST":
		if strings.TrimSpace(value) == "" {
			return errors.New("host 不能为空")
		}
	case "AGENTDOCK_PORT":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 1 || n > 65535 {
			return errors.New("port 必须是 1-65535 的整数")
		}
	case "AGENTDOCK_LOG_LEVEL":
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "debug", "info", "warn", "error":
		default:
			return errors.New("log_level 必须是 debug/info/warn/error")
		}
	case "AGENTDOCK_MCP_APPS_MODE":
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "full", "compact", "off":
		default:
			return errors.New("mcp_apps_mode 必须是 full/compact/off")
		}
	case "AGENTDOCK_BROWSER_ENABLED", "AGENTDOCK_BROWSER_REUSE_EXISTING_CDP", "AGENTDOCK_ACP_ENABLED", "AGENTDOCK_NETWORK_CAPTURE_ENABLED", "AGENTDOCK_MANUAL_LLM_ENABLED":
		if _, err := strconv.ParseBool(strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("%s 必须是 true/false", key)
		}
	case "AGENTDOCK_TRACE_MODE":
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "disk", "memory", "off":
		default:
			return errors.New("trace_mode 必须是 disk/memory/off")
		}
	case "AGENTDOCK_TRACE_RETENTION_DAYS":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 1 || n > 3650 {
			return errors.New("trace_retention_days 必须是 1-3650 的整数")
		}
	case "AGENTDOCK_NETWORK_CAPTURE_MAX":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 1 || n > 100000 {
			return errors.New("network_capture_max 必须是 1-100000 的整数")
		}
	case "AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES":
		if err := validateIntRange(value, manualllm.ConfigMinPreview, manualllm.ConfigMaxPreview, "preview_bytes"); err != nil {
			return err
		}
	case "AGENTDOCK_MANUAL_LLM_SLICE_BYTES", "AGENTDOCK_MANUAL_LLM_MAX_SLICE_BYTES":
		if err := validateIntRange(value, manualllm.ConfigMinSliceBytes, manualllm.ConfigMaxSliceBytes, "slice_bytes"); err != nil {
			return err
		}
	case "AGENTDOCK_MANUAL_LLM_WAIT_MS", "AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS":
		if err := validateIntRange(value, manualllm.ConfigMinWaitTimeoutMs, manualllm.ConfigMaxWaitTimeoutMs, "wait_ms"); err != nil {
			return err
		}
	case "AGENTDOCK_MANUAL_LLM_MAX_PENDING":
		if err := validateIntRange(value, 1, manualllm.ConfigMaxPendingQueue, "max_pending"); err != nil {
			return err
		}
	case "AGENTDOCK_MANUAL_LLM_CACHE_MAX_RESPONSES":
		if err := validateIntRange(value, manualllm.ConfigMinCacheResponses, manualllm.ConfigMaxCacheResponses, "cache_max_responses"); err != nil {
			return err
		}
	case "AGENTDOCK_MANUAL_LLM_CACHE_TTL_SEC":
		if err := validateIntRange(value, int(manualllm.ConfigMinCacheTTL.Seconds()), int(manualllm.ConfigMaxCacheTTL.Seconds()), "cache_ttl_sec"); err != nil {
			return err
		}
	case "AGENTDOCK_MANUAL_LLM_KEEPALIVE_MS":
		if err := validateIntRange(value, int(manualllm.ConfigMinKeepalive.Milliseconds()), int(manualllm.ConfigMaxKeepalive.Milliseconds()), "keepalive_ms"); err != nil {
			return err
		}
	case "AGENTDOCK_MANUAL_LLM_CACHE_MAX_IMAGE_MB":
		if err := validateIntRange(value, 1, manualllm.ConfigMaxRetainedImageMB, "cache_max_image_mb"); err != nil {
			return err
		}
	case "AGENTDOCK_BROWSER_CDP_URL", "AGENTDOCK_ACP_DEFAULT_PROFILE":
		// 自由文本，允许为空。
	}
	return nil
}

func validateIntRange(value string, minVal, maxVal int, label string) error {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < minVal || n > maxVal {
		return fmt.Errorf("%s 必须是 %d-%d 的整数", label, minVal, maxVal)
	}
	return nil
}

func isEditableConfigKey(key string) bool {
	for _, k := range editableConfigKeys {
		if k == key {
			return true
		}
	}
	return false
}

// RuntimeConfigUpdate 原子更新 env 文件：保留未知 key，只覆盖白名单里的值。
func (r *Runtime) RuntimeConfigUpdate(updates map[string]string) (Result, error) {
	path, err := configEnvFilePath()
	if err != nil {
		return nil, err
	}
	if len(updates) == 0 {
		return nil, errors.New("没有可更新的配置")
	}
	for key, value := range updates {
		if !isEditableConfigKey(key) {
			return nil, fmt.Errorf("不允许修改配置项 %s", key)
		}
		if err := validateConfigUpdate(key, value); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
	}
	values, err := envstore.ParseFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	if values == nil {
		values = map[string]string{}
	}
	for key, value := range updates {
		values[key] = value
	}
	if err := r.validateManualLLMConfigCrossFields(values); err != nil {
		return nil, err
	}
	if err := atomicfile.Write(path, envstore.Marshal(values), 0o600); err != nil {
		return nil, fmt.Errorf("写入配置失败: %w", err)
	}
	return Result{
		"ok":      true,
		"source":  runtimeAPISource,
		"updated": len(updates),
		"hint":    "配置已保存，重启 Core 后生效。",
	}, nil
}

func (r *Runtime) validateManualLLMConfigCrossFields(values map[string]string) error {
	opts := r.manualLLMOptions()
	intOrDefault := func(key string, fallback int) int {
		raw := strings.TrimSpace(values[key])
		if raw == "" {
			return fallback
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fallback
		}
		return n
	}
	sliceBytes := intOrDefault("AGENTDOCK_MANUAL_LLM_SLICE_BYTES", opts.SliceBytes)
	maxSliceBytes := intOrDefault("AGENTDOCK_MANUAL_LLM_MAX_SLICE_BYTES", opts.MaxSliceBytes)
	if sliceBytes > maxSliceBytes {
		return fmt.Errorf("AGENTDOCK_MANUAL_LLM_SLICE_BYTES (%d) 不能大于 AGENTDOCK_MANUAL_LLM_MAX_SLICE_BYTES (%d)", sliceBytes, maxSliceBytes)
	}
	waitMs := intOrDefault("AGENTDOCK_MANUAL_LLM_WAIT_MS", opts.DefaultWaitMs)
	maxWaitMs := intOrDefault("AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS", opts.MaxWaitMs)
	if waitMs > maxWaitMs {
		return fmt.Errorf("AGENTDOCK_MANUAL_LLM_WAIT_MS (%d) 不能大于 AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS (%d)", waitMs, maxWaitMs)
	}
	return nil
}

// RuntimeConfigRestart 重启 Core。只有 launchd/SMAppService 管理的实例能重启；
// 手动启动的实例会明确报错，而不是杀掉后拉不回来。
//
// 必须先回响应再触发重启：kickstart 会停掉当前 job，而处理本请求的正是这个
// 进程——同步重启会让 HTTP 连接在响应写出前被切断，前端只看到网络错误。
// 因此校验与前置检查同步做（失败可即时返回），真正的重启延迟到响应之后。
func (r *Runtime) RuntimeConfigRestart(ctx context.Context) (Result, error) {
	root, err := configRuntimeRoot()
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("定位可执行文件失败: %w", err)
	}
	go func() {
		// 给 HTTP 层留出写出响应的时间窗口。
		time.Sleep(700 * time.Millisecond)
		cmd := exec.Command(self, "service", "restart", "--runtime-root", root)
		if out, err := cmd.CombinedOutput(); err != nil {
			slog.Warn("deferred core restart failed", "error", err, "output", strings.TrimSpace(string(out)))
		}
	}()
	return Result{"ok": true, "source": runtimeAPISource, "message": "Core 正在重启，页面将短暂中断后恢复"}, nil
}
