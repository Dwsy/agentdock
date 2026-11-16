package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"

	console "github.com/uvwt/agentdock/desktop/console/internal/ui"
)

// themePrefFile 是外观模式偏好文件的文件名，落在 mygo 的 PathUserData
// （mygo 自身的用户偏好如 locale 也存这里）。mygo 没有通用键值配置存储，
// 文件就是框架惯例的持久化方式。
const themePrefFile = "theme"

// loadThemeMode 读取用户上次选择的外观模式；文件缺失、不可读或内容未知都
// 回退默认（浅色），不阻塞启动。
func loadThemeMode() console.ThemeMode {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return console.ThemeModeLight
	}
	data, err := os.ReadFile(filepath.Join(dir, themePrefFile))
	if err != nil {
		return console.ThemeModeLight
	}
	return console.ParseThemeMode(strings.TrimSpace(string(data)))
}

// saveThemeMode 尽力持久化外观模式：写临时文件后 rename（原子替换，偏好文件
// 被占用或磁盘出错时静默放弃——偏好丢了下次在界面再选一次即可，不值得为它
// 弹错）。
func saveThemeMode(m console.ThemeMode) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return
	}
	path := filepath.Join(dir, themePrefFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(m.String()+"\n"), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
	}
}
