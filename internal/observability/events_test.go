package observability

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestEventBusDeliversPublishedEventsToSubscriber(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := bus.Subscribe(ctx)

	bus.Publish(EventKindCall, CallEvent{ID: 1, Tool: "exec_command", Source: "mcp", Success: true})

	select {
	case event := <-events:
		if event.Kind != EventKindCall || event.At.IsZero() {
			t.Fatalf("event = %#v", event)
		}
		payload, ok := event.Payload.(CallEvent)
		if !ok || payload.ID != 1 || payload.Tool != "exec_command" {
			t.Fatalf("payload = %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published event")
	}
	if bus.SubscriberCount() != 1 {
		t.Fatalf("subscribers = %d, want 1", bus.SubscriberCount())
	}
}

func TestEventBusDropsOldestEventsWhenBufferFull(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := bus.Subscribe(ctx)

	const total = eventSubscriberBuffer + 10
	for index := 1; index <= total; index++ {
		bus.Publish(EventKindTask, TaskEvent{ID: string(rune('a' + index))})
	}
	if got := bus.Dropped(); got != 10 {
		t.Fatalf("dropped = %d, want 10", got)
	}

	// 缓冲保留的是最新 eventSubscriberBuffer 条：最旧 10 条被丢弃。
	first := <-events
	if first.Payload.(TaskEvent).ID != string(rune('a'+11)) {
		t.Fatalf("oldest surviving event = %#v, want id %q", first, string(rune('a'+11)))
	}
	for index := 0; index < eventSubscriberBuffer-1; index++ {
		<-events
	}
	select {
	case extra := <-events:
		t.Fatalf("unexpected extra event %#v", extra)
	default:
	}
}

func TestEventBusPublishDoesNotBlockOnSlowSubscriber(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = bus.Subscribe(ctx) // 永不读取：缓冲满后必须丢弃而不是阻塞。

	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := 0; index < 10000; index++ {
			bus.Publish(EventKindCall, CallEvent{ID: uint64(index)})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on slow subscriber")
	}
	if bus.Dropped() == 0 {
		t.Fatal("expected dropped events to be counted")
	}
}

func TestEventBusUnsubscribesOnContextCancel(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	events := bus.Subscribe(ctx)

	cancel()
	deadline := time.After(time.Second)
	for {
		select {
		case _, open := <-events:
			if !open {
				waitFor(t, func() bool { return bus.SubscriberCount() == 0 }, "subscriber did not unsubscribe after cancel")
				return
			}
		case <-deadline:
			t.Fatal("subscription channel was not closed after context cancel")
		}
	}
}

func TestEventBusCloseShutsDownSubscribers(t *testing.T) {
	bus := NewEventBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := bus.Subscribe(ctx)

	bus.Close()
	if _, open := <-events; open {
		t.Fatal("subscriber channel should be closed after Close")
	}
	if bus.SubscriberCount() != 0 {
		t.Fatalf("subscribers = %d after Close", bus.SubscriberCount())
	}
	bus.Publish(EventKindPing, nil) // 关闭后的发布必须是空操作而不是 panic。
	if bus.Dropped() != 0 {
		t.Fatalf("dropped = %d after close publish", bus.Dropped())
	}
}

func TestEventBusSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	bus := NewEventBus()
	bus.Close()
	events := bus.Subscribe(context.Background())
	if _, open := <-events; open {
		t.Fatal("subscribe after close must return a closed channel")
	}
}

func TestEventBusConcurrentSubscribePublishUnsubscribe(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 50; iteration++ {
				ctx, cancel := context.WithCancel(context.Background())
				events := bus.Subscribe(ctx)
				bus.Publish(EventKindArtifact, ArtifactEvent{Action: "publish", ArtifactID: "a"})
				cancel()
				for range events { // 等通道关闭，确保退订完成。
				}
			}
		}(worker)
	}
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 200; iteration++ {
				bus.Publish(EventKindCall, CallEvent{ID: uint64(iteration)})
			}
		}()
	}
	wg.Wait()
	waitFor(t, func() bool { return bus.SubscriberCount() == 0 }, "subscribers leaked after concurrent use")
}

func TestEventBusMethodsAreNilSafe(t *testing.T) {
	var bus *EventBus
	bus.Publish(EventKindPing, nil)
	bus.Close()
	if bus.SubscriberCount() != 0 || bus.Dropped() != 0 {
		t.Fatal("nil bus must report zero subscribers and drops")
	}
	if _, open := <-bus.Subscribe(context.Background()); open {
		t.Fatal("nil bus subscribe must return a closed channel")
	}
}

func TestEventBusPublishDoesNotLeakGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	bus := NewEventBus()
	for index := 0; index < 20; index++ {
		ctx, cancel := context.WithCancel(context.Background())
		_ = bus.Subscribe(ctx)
		cancel()
	}
	bus.Close()
	waitFor(t, func() bool { return bus.SubscriberCount() == 0 }, "subscribers leaked")
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before+3 }, "subscriber watcher goroutines leaked")
}

func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(message)
}
