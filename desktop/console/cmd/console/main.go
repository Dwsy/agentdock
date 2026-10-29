// Command console 是 AgentDock Console 的入口：装配 runtimeapi 客户端、
// MyGo native UI 页面层与主窗口。
package main

import (
	"fmt"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	console "github.com/uvwt/agentdock/desktop/console/internal/ui"
)

// defaultBaseURL 是 LoadConfig 失败时的回退地址：页面会自然进入错误态，
// 由用户在运行时服务恢复后点“重试”，不在启动期 fatal。
const defaultBaseURL = "http://127.0.0.1:8765"

func main() {
	baseURL, token, err := runtimeapi.LoadConfig()
	if err != nil || baseURL == "" {
		baseURL = defaultBaseURL
		token = ""
	}
	client := runtimeapi.NewClient(baseURL, token)

	app := console.New(client)
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "AgentDock Console",
			Width:     1180,
			Height:    740,
			MinWidth:  900,
			MinHeight: 600,
			Content:   ui.View(app.View),
		})
		app.SetInvalidator(win.Invalidate)
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "console:", err)
		os.Exit(1)
	}
}
