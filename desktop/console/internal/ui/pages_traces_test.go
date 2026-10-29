package ui

import (
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
)

// countingBackend 在 stubBackend 之上统计 Traces() 拉取次数，验证追踪页轮询。
type countingBackend struct {
	stubBackend
	mu          sync.Mutex
	tracesCalls int
}

func (b *countingBackend) Traces(query, tool, source, status string, limit int) (*runtimeapi.Traces, error) {
	b.mu.Lock()
	b.tracesCalls++
	b.mu.Unlock()
	return b.stubBackend.Traces(query, tool, source, status, limit)
}

func (b *countingBackend) traces() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tracesCalls
}

// startTraces 构造 App 并切到追踪页，返回后已拉取一次（进入页面的页切换拉取）。
func startTraces(t *testing.T, backend runtimeapi.Backend) (*App, *ui.Tester, chan struct{}, *countingBackend) {
	t.Helper()
	cb := backend.(*countingBackend)
	app, tt, notified := newTestApp(t, cb)
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	if err := tt.Click("追踪"); err != nil {
		t.Fatalf("click 追踪: %v", err)
	}
	waitNotify(t, notified, "traces initial fetch")
	tt.Frame()
	if got := cb.traces(); got != 1 {
		t.Fatalf("traces fetches after page switch = %d, want 1", got)
	}
	return app, tt, notified, cb
}

// TestTracesAutoPollFetches 开自动刷新（默认开）时，tt.Advance(5s+) 触发后台重新拉取。
func TestTracesAutoPollFetches(t *testing.T) {
	status, overview, analytics := testOverviewData()
	backend := &countingBackend{stubBackend: stubBackend{
		status: status, overview: overview, analytics: analytics,
	}}
	app, tt, notified, cb := startTraces(t, backend)

	if !app.traceAuto {
		t.Fatal("auto refresh should default to on")
	}
	if !tt.HasText("上次刷新") {
		t.Error("toolbar missing last refresh time")
	}

	// 推进 5s+：After 的定时帧到来，渲染时发现过期即触发后台拉取。
	tt.Advance(5*time.Second + time.Millisecond)
	waitNotify(t, notified, "auto poll fetch")
	tt.Frame()
	if got := cb.traces(); got != 2 {
		t.Fatalf("traces fetches after Advance(5s) = %d, want 2", got)
	}

	// 再推进 5s+：再拉一次（时间戳去重：每 5s 至多一次）。
	tt.Advance(5*time.Second + time.Millisecond)
	waitNotify(t, notified, "auto poll fetch 2")
	tt.Frame()
	if got := cb.traces(); got != 3 {
		t.Fatalf("traces fetches after second Advance(5s) = %d, want 3", got)
	}
}

// TestTracesAutoPollSwitchOff 关闭"自动刷新"开关后，Advance 不再拉取。
func TestTracesAutoPollSwitchOff(t *testing.T) {
	status, overview, analytics := testOverviewData()
	backend := &countingBackend{stubBackend: stubBackend{
		status: status, overview: overview, analytics: analytics,
	}}
	app, tt, notified, cb := startTraces(t, backend)

	// 先验证轮询在跑，再关掉开关。
	tt.Advance(5*time.Second + time.Millisecond)
	waitNotify(t, notified, "auto poll fetch")
	tt.Frame()
	if got := cb.traces(); got != 2 {
		t.Fatalf("traces fetches before switching off = %d, want 2", got)
	}

	if err := tt.Click("自动刷新开关"); err != nil {
		t.Fatalf("click 自动刷新开关: %v", err)
	}
	tt.Frame()
	if app.traceAuto {
		t.Fatal("switch did not turn auto refresh off")
	}

	tt.Advance(30 * time.Second)
	// 后台拉取是异步的：给误触发的拉取留出跑完的时间再验证计数。
	time.Sleep(50 * time.Millisecond)
	if got := cb.traces(); got != 2 {
		t.Fatalf("traces fetches after disabling auto refresh = %d, want 2", got)
	}
}
