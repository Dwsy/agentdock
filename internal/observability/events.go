package observability

import (
	"context"
	"sync"
	"time"
)

// 运行时事件种类。ping 只由传输层保活帧产生，不进入总线。
const (
	EventKindCall     = "call"
	EventKindTask     = "task"
	EventKindArtifact = "artifact"
	EventKindPing     = "ping"
)

// eventSubscriberBuffer 是每个订阅者的有界缓冲深度：慢消费者不能让
// 发布方（recorder 热路径）阻塞，缓冲满时丢弃最旧事件并计数。
const eventSubscriberBuffer = 64

// Event 是进程内运行时事件信封。Payload 是强类型小投影
// （CallEvent/TaskEvent/ArtifactEvent），不携带参数、结果或路径。
type Event struct {
	Kind    string    `json:"kind"`
	At      time.Time `json:"at"`
	Payload any       `json:"payload,omitempty"`
}

// CallEvent 是 observeToolCall 汇聚点发布的工具调用摘要。
// 字段与 ExecutionRecord 的零 Payload 投影保持一致，不扩大暴露面。
type CallEvent struct {
	ID         uint64    `json:"id"`
	Tool       string    `json:"tool"`
	Source     string    `json:"source"`
	TaskID     string    `json:"task_id"`
	Success    bool      `json:"success"`
	DurationMS float64   `json:"duration_ms"`
	ErrorCode  string    `json:"error_code"`
	StartedAt  time.Time `json:"started_at"`
}

// TaskEvent 是任务状态迁移（checkpoint/complete/delete）的摘要投影。
type TaskEvent struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Phase     string    `json:"phase"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ArtifactEvent 是发布物 publish/revoke/extend 写操作摘要。
type ArtifactEvent struct {
	Action     string `json:"action"`
	ArtifactID string `json:"artifact_id"`
}

type eventSubscriber struct {
	id      uint64
	ch      chan Event
	dropped uint64
}

// EventBus 是进程内事件总线。发布永不阻塞调用方：每个订阅者独立有界
// 缓冲，满则丢弃最旧事件并累加 dropped 计数。
// 零值不可用，用 NewEventBus 创建；所有方法对 nil 接收者安全。
type EventBus struct {
	mu      sync.Mutex
	nextID  uint64
	subs    map[uint64]*eventSubscriber
	dropped uint64
	closed  bool
	done    chan struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{
		subs: make(map[uint64]*eventSubscriber),
		done: make(chan struct{}),
	}
}

// Subscribe 注册订阅者并返回只读事件通道。ctx 取消或总线 Close 后
// 通道被关闭并退订；调用方不需要显式退订。
func (b *EventBus) Subscribe(ctx context.Context) <-chan Event {
	if b == nil {
		return closedEventChannel()
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return closedEventChannel()
	}
	b.nextID++
	subscriber := &eventSubscriber{id: b.nextID, ch: make(chan Event, eventSubscriberBuffer)}
	b.subs[subscriber.id] = subscriber
	b.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
		case <-b.done:
		}
		b.unsubscribe(subscriber.id)
	}()
	return subscriber.ch
}

// Publish 向所有订阅者广播事件。非阻塞：慢订阅者只丢自己的最旧事件。
func (b *EventBus) Publish(kind string, payload any) {
	if b == nil {
		return
	}
	event := Event{Kind: kind, At: time.Now().UTC(), Payload: payload}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, subscriber := range b.subs {
		select {
		case subscriber.ch <- event:
			continue
		default:
		}
		// 缓冲满：丢弃最旧一条为新事件腾位，两次尝试都失败才放弃新事件。
		select {
		case <-subscriber.ch:
			b.dropped++
			subscriber.dropped++
		default:
		}
		select {
		case subscriber.ch <- event:
		default:
			b.dropped++
			subscriber.dropped++
		}
	}
}

// Dropped 返回总线启动以来因缓冲满被丢弃的事件总数。
func (b *EventBus) Dropped() uint64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// SubscriberCount 返回当前订阅者数量，用于断连退订断言与诊断。
func (b *EventBus) SubscriberCount() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Close 退订并关闭全部订阅者通道；之后的 Publish 变成空操作。
func (b *EventBus) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := b.subs
	b.subs = make(map[uint64]*eventSubscriber)
	close(b.done)
	b.mu.Unlock()
	for _, subscriber := range subs {
		close(subscriber.ch)
	}
}

func (b *EventBus) unsubscribe(id uint64) {
	b.mu.Lock()
	subscriber, ok := b.subs[id]
	if ok {
		delete(b.subs, id)
	}
	b.mu.Unlock()
	if ok {
		close(subscriber.ch)
	}
}

func closedEventChannel() <-chan Event {
	ch := make(chan Event)
	close(ch)
	return ch
}
