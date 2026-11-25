//go:build darwin

package desktopruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func platformPrepareLaunchEnvironment(component string) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fmt.Errorf("解析 macOS 用户目录失败: %w", err)
	}
	workDir := filepath.Join(home, "AgentDock")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("创建 AgentDock 工作目录失败: %w", err)
	}
	if err := os.Chdir(workDir); err != nil {
		return fmt.Errorf("进入 AgentDock 工作目录失败: %w", err)
	}

	logDir := filepath.Join(home, "Library", "Logs", "AgentDock")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("创建 AgentDock 日志目录失败: %w", err)
	}
	if component == "core" {
		// launchd 默认只有系统 PATH，直接查找 rg 和 fd 的文件工具看不到 Homebrew。
		path := filepath.SplitList(os.Getenv("PATH"))
		for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
			if !slices.Contains(path, dir) {
				path = append(path, dir)
			}
		}
		if err := os.Setenv("PATH", strings.Join(path, string(os.PathListSeparator))); err != nil {
			return fmt.Errorf("补充 macOS Core PATH 失败: %w", err)
		}
	}
	// Core 与 Tunnel 都由进程内轮转 writer 接管日志，避免 launchd 固定文件无限增长。
	return nil
}
