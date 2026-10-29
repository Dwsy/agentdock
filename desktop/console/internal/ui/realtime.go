package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// 实时事件流（SSE）接入：backend 实现 runtimeapi.EventsOpener（即 runtimeapi.Client）
// 时 New 启动 runEvents 后台循环——OpenEvents→Recv 循环消费 call/task/artifact 事件，
// 按 kind 惰性失效对应页缓存并请求重绘；ping 只记保活时间戳；断线后按指数退避
// 重连（1s→2s→…→30s 封顶），重连成功后全量刷新一次。SSE 正常时追踪页停止
// 5s 轮询，断开/未接入时轮询兜底。顶栏指示灯："实时"（SSE 正常）/ "轮询"
// （断开或未接入但轮询兜底能取到数据）/ "重连中…"（完全拿不到数据且在重连，
// 旧 Core 无 /internal/runtime/events 时属于前者，不该误报）。

const (
	// rtRetryInitial / rtRetryMax 是断线重连的指数退避参数。
	rtRetryInitial = time.Second
	rtRetryMax     = 30 * time.Second
	// rtEventLimit 是概览页"实时事件"时间线保留与展示的最大条数。
	rtEventLimit = 10
)

// rtEvent 是实时事件时间线的一条记录（时间 + kind + 摘要）。
type rtEvent struct {
	At      time.Time
	Kind    string
	Summary string
}

// runEvents 是实时事件流的后台循环，直到 Close/Stop（rtCtx 取消）退出。
func (a *App) runEvents() {
	defer a.bgWG.Done()
	backoff := rtRetryInitial
	for {
		if a.rtCtx.Err() != nil {
			return
		}
		stream, err := a.openEvents(a.rtCtx)
		if err != nil {
			if a.rtCtx.Err() != nil {
				return
			}
			a.setRealtime(false)
			if !a.sleepRetry(backoff) {
				return
			}
			backoff = min(backoff*2, rtRetryMax)
			continue
		}
		// 连接成功：恢复实时指示灯；重连（此前已有会话）成功后全量刷新一次。
		a.setRealtime(true)
		if a.markConnected() {
			a.refreshAll()
		}
		backoff = rtRetryInitial
		_ = a.recvLoop(stream)
		stream.Close()
		if a.rtCtx.Err() != nil {
			return
		}
		// 断线：降级指示灯并退避重连。
		a.setRealtime(false)
		if !a.sleepRetry(backoff) {
			return
		}
		backoff = min(backoff*2, rtRetryMax)
	}
}

// recvLoop 持续消费事件帧直到断线/停止。
func (a *App) recvLoop(stream runtimeapi.Events) error {
	for {
		ev, err := stream.Recv()
		if err != nil {
			return err
		}
		a.handleEvent(ev)
	}
}

// sleepRetry 退避等待；返回 false 表示已停止（Close/Stop）。
func (a *App) sleepRetry(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-a.rtCtx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// setRealtime 更新 SSE 连接状态；状态变化时请求重绘（顶栏指示灯随之变化）。
func (a *App) setRealtime(ok bool) {
	a.mu.Lock()
	changed := a.realtime != ok
	a.realtime = ok
	a.mu.Unlock()
	if changed {
		a.notify()
	}
}

// markConnected 记录连接成功，返回是否为重连（此前已有会话）。
func (a *App) markConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	reconnected := a.rtConnected
	a.rtConnected = true
	return reconnected
}

// refreshAll 全量刷新所有页面缓存（重连成功后调用一次）。
func (a *App) refreshAll() {
	for _, page := range []string{
		pageOverview, pageAnalytics, pageTraces,
		pageCatalog, pageFiles, pagePublished, pageTasks,
	} {
		a.reload(page)
	}
}

// realtimeIndicator 返回顶栏实时连接指示灯的文案与色调：
// SSE 正常 → "实时"（accent）；SSE 断开/未接入但轮询兜底能取到数据 → "轮询"
// （neutral）；完全拿不到数据且在重连 → "重连中…"（warning）。
// 旧 Core 没有 /internal/runtime/events：SSE 永远连不上但 REST 轮询有数据，
// 显示"轮询"而不是吓人的"重连中…"。
func (a *App) realtimeIndicator() (string, kit.Tone) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.realtime:
		return "实时", kit.ToneAccent
	case a.openEvents == nil || a.pollOK:
		return "轮询", kit.ToneNeutral
	default:
		return "重连中…", kit.ToneWarning
	}
}

// handleEvent 处理一帧 SSE 事件：call/task/artifact 记入时间线并按 kind
// 惰性失效对应页缓存（下次渲染时 ensureLoaded 重拉）；ping 仅记保活时间戳；
// 随后节流拉取告警并请求重绘。未知 kind 忽略。
func (a *App) handleEvent(ev runtimeapi.SSEEvent) {
	switch ev.Kind {
	case runtimeapi.EventPing:
		a.mu.Lock()
		a.rtPingAt = time.Now()
		a.mu.Unlock()
		return
	case runtimeapi.EventCall, runtimeapi.EventTask, runtimeapi.EventArtifact:
	default:
		return
	}
	a.mu.Lock()
	a.events = append([]rtEvent{{At: eventTime(ev), Kind: ev.Kind, Summary: eventSummary(ev)}}, a.events...)
	if len(a.events) > rtEventLimit {
		a.events = a.events[:rtEventLimit]
	}
	a.mu.Unlock()
	switch ev.Kind {
	case runtimeapi.EventCall:
		a.invalidatePage(pageOverview)
		a.invalidatePage(pageAnalytics)
		a.invalidatePage(pageTraces)
	case runtimeapi.EventTask:
		a.invalidatePage(pageOverview)
		a.invalidatePage(pageTasks)
	case runtimeapi.EventArtifact:
		a.invalidatePage(pagePublished)
	}
	a.maybeFetchAlerts()
	a.notify()
}

// invalidatePage 惰性失效一页缓存：保留旧数据继续展示，下次渲染时重拉。
func (a *App) invalidatePage(page string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ls := a.loadOf(page); ls != nil {
		ls.loaded = false
		ls.err = nil
	}
}

// eventTime 返回事件时间：call 用 started_at、task 用 updated_at，
// artifact 无时间字段用到达时间。
func eventTime(ev runtimeapi.SSEEvent) time.Time {
	switch ev.Kind {
	case runtimeapi.EventCall:
		if e, err := ev.DecodeCall(); err == nil && !e.StartedAt.IsZero() {
			return e.StartedAt
		}
	case runtimeapi.EventTask:
		if e, err := ev.DecodeTask(); err == nil && !e.UpdatedAt.IsZero() {
			return e.UpdatedAt
		}
	}
	return time.Now()
}

// eventSummary 生成事件时间线的单行摘要（工具/状态/耗时、任务标题/状态、
// 产物动作等）；负载解析失败时给出占位文案。
func eventSummary(ev runtimeapi.SSEEvent) string {
	switch ev.Kind {
	case runtimeapi.EventCall:
		if e, err := ev.DecodeCall(); err == nil {
			s := fmt.Sprintf("%s · %s · %s", e.Tool, successLabel(e.Success), kit.FormatDuration(e.DurationMS))
			if e.ErrorCode != "" {
				s += " · " + e.ErrorCode
			}
			return s
		}
	case runtimeapi.EventTask:
		if e, err := ev.DecodeTask(); err == nil {
			parts := make([]string, 0, 3)
			for _, p := range []string{e.Title, e.Status, e.Phase} {
				if p != "" {
					parts = append(parts, p)
				}
			}
			return strings.Join(parts, " · ")
		}
	case runtimeapi.EventArtifact:
		if e, err := ev.DecodeArtifact(); err == nil {
			return e.Action + " · " + e.ArtifactID
		}
	}
	return "（无法解析的事件负载）"
}

// eventTone 把事件 kind 映射为时间线 chip 的 kit 色调。
func eventTone(kind string) kit.Tone {
	switch kind {
	case runtimeapi.EventCall:
		return kit.ToneAccent
	case runtimeapi.EventTask:
		return kit.ToneSuccess
	case runtimeapi.EventArtifact:
		return kit.ToneWarning
	}
	return kit.ToneNeutral
}
