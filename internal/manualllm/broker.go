package manualllm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/observability"
)

var (
	// ErrBrokerClosed is returned when operations run after Broker.Close.
	ErrBrokerClosed = errors.New("manual LLM broker is closed")
	// ErrQueueFull is returned when too many un-claimed /v1/responses requests await manual work.
	ErrQueueFull = errors.New("manual LLM request queue is full")
)

type streamDelivery struct {
	event     *Event
	terminate bool
	reason    string
	errCode   string
	errMsg    string
}

type requestRecord struct {
	ID                 string
	ResponseID         string
	SessionID          string
	ConversationKey    string
	Model              string
	Status             RequestStatus
	Turn               int
	CreatedAt          time.Time
	ClaimedAt          time.Time
	CompletedAt        time.Time
	PreviousResponseID string

	SystemPrompt string
	SystemHash   string
	Tools        []ToolDef
	ToolsHash    string
	ToolHashes   map[string]string
	AllowedTools map[string]ToolDef
	ToolChoice   any
	TextFormat   any

	FullMessages     []Message
	NewMessages      []Message
	NewIndices       []int
	CommittedAfter   []Message
	Images           map[string]ImageRef
	ClaimedCallerKey string

	pendingText     strings.Builder
	emittedMessages []Message
	emittedCalls    int
	callSeq         int
	seenResponseIDs map[string]struct{}
	ErrorMessage    string

	streamQueue  []streamDelivery
	streamNotify chan struct{}
	doneCh       chan struct{}
}

type sessionState struct {
	SessionID           string
	ConversationKey     string
	Turns               int
	CommittedMessages   []Message
	LastResponseID      string
	ConfirmedCallerKey  string
	ConfirmedSystemHash string
	ConfirmedToolsHash  string
	ConfirmedToolHashes map[string]string
	ConfirmedGuide      bool
	UpdatedAt           time.Time
}

// knownModel 是模型注册表内部条目：lastSeen 用于有界淘汰与排序。
type knownModel struct {
	id       string
	requests int
	created  time.Time
	lastSeen time.Time
}

// Broker coordinates manual LLM turn queueing, progressive retrieval, and SSE streaming.
type Broker struct {
	mu             sync.Mutex
	opts           Options
	closed         bool
	closedCh       chan struct{}
	waitNotify     chan struct{}
	reqSeq         uint64
	sessSeq        uint64
	queue          []*requestRecord
	requests       map[string]*requestRecord
	responses      map[string]*requestRecord
	sessions       map[string]*sessionState
	convSessions   map[string]*sessionState
	sessionWaiters map[string]int
	// models 只登记真实请求体里观测到的模型名（GET /v1/models 的数据源）。
	models map[string]*knownModel
}

// NewBroker constructs an active Broker using DefaultOptions.
func NewBroker() *Broker {
	return NewBrokerWithOptions(DefaultOptions())
}

// NewBrokerWithOptions constructs an active Broker with normalized tuning parameters.
func NewBrokerWithOptions(opts Options) *Broker {
	return &Broker{
		opts:           opts.Normalized(),
		closedCh:       make(chan struct{}),
		waitNotify:     make(chan struct{}),
		requests:       make(map[string]*requestRecord),
		responses:      make(map[string]*requestRecord),
		sessions:       make(map[string]*sessionState),
		convSessions:   make(map[string]*sessionState),
		sessionWaiters: make(map[string]int),
		models:         make(map[string]*knownModel),
	}
}

// Options returns a copy of the broker's normalized tuning configuration.
func (b *Broker) Options() Options {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.opts
}

// UpdateOptions updates live broker tuning parameters without dropping active sessions.
func (b *Broker) UpdateOptions(opts Options) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.opts = opts.Normalized()
}

// Close shuts down the broker and terminates any waiting requests or HTTP streams.
func (b *Broker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	now := time.Now().UTC()
	for _, req := range b.requests {
		if req.Status == StatusQueued || req.Status == StatusClaimed || req.Status == StatusStreaming {
			req.Status = StatusCancelled
			req.ErrorMessage = "server shutting down"
			req.CompletedAt = now
			req.pushTerminateLocked("server_shutdown", req.ErrorMessage)
			closeDoneOnce(&req.doneCh)
		}
	}
	close(b.closedCh)
	b.notifyWaitersLocked()
	b.mu.Unlock()
}

func (b *Broker) notifyWaitersLocked() {
	close(b.waitNotify)
	b.waitNotify = make(chan struct{})
}

func (req *requestRecord) notifyStreamLocked() {
	close(req.streamNotify)
	req.streamNotify = make(chan struct{})
}

func (req *requestRecord) pushTerminateLocked(code, msg string) {
	req.streamQueue = append(req.streamQueue, streamDelivery{
		terminate: true,
		errCode:   code,
		errMsg:    msg,
	})
	req.notifyStreamLocked()
}

func closeDoneOnce(ch *chan struct{}) {
	select {
	case <-*ch:
	default:
		close(*ch)
	}
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// ServeResponses enqueues an incoming /v1/responses request and streams SSE until completion.
func (b *Broker) ServeResponses(ctx context.Context, w http.ResponseWriter, body []byte) {
	parsed, err := ParseResponsesRequest(body)
	if err != nil {
		var respErr *ResponsesError
		if errors.As(err, &respErr) {
			respErr.WriteJSON(w)
			return
		}
		(&ResponsesError{
			StatusCode: http.StatusBadRequest,
			Type:       "invalid_request_error",
			Code:       "bad_request",
			Message:    err.Error(),
		}).WriteJSON(w)
		return
	}

	req, err := b.enqueue(parsed)
	if err != nil {
		status := http.StatusServiceUnavailable
		code := "broker_unavailable"
		if errors.Is(err, ErrQueueFull) {
			status = http.StatusTooManyRequests
			code = "queue_full"
		}
		(&ResponsesError{
			StatusCode: status,
			Type:       "server_error",
			Code:       code,
			Message:    err.Error(),
		}).WriteJSON(w)
		return
	}

	opts := b.Options()
	inTokens := observability.CountTextTokensWithMode(string(body), opts.TokenCountMode)
	sse := NewLiveSSEWriter(w, req.ResponseID, req.Model, req.CreatedAt.Unix(), req.Tools, inTokens, opts.TokenCountMode)
	sse.Start()

	ticker := time.NewTicker(opts.KeepaliveInterval)
	defer ticker.Stop()

	drainAndApply := func(cursor *int) bool {
		batch, doneSignal, _ := b.drainStream(req, cursor)
		for _, item := range batch {
			if item.event != nil {
				_, stopReason, errMsg := sse.Apply(*item.event)
				if errMsg != "" {
					sse.Fail("manual_llm_error", errMsg)
					return true
				}
				if stopReason != "" {
					sse.Complete(stopReason)
					return true
				}
			}
			if item.terminate {
				if item.reason != "" {
					sse.Complete(item.reason)
				} else {
					sse.Fail(item.errCode, item.errMsg)
				}
				return true
			}
		}
		if doneSignal != nil {
			if doneSignal.reason != "" {
				sse.Complete(doneSignal.reason)
			} else {
				sse.Fail(doneSignal.errCode, doneSignal.errMsg)
			}
			return true
		}
		return false
	}

	cursor := 0
	for {
		if drainAndApply(&cursor) {
			return
		}
		b.mu.Lock()
		notifyCh := req.streamNotify
		b.mu.Unlock()

		select {
		case <-notifyCh:
		case <-ticker.C:
			sse.Keepalive()
		case <-ctx.Done():
			b.cancelOnDisconnect(req.ID)
			return
		case <-b.closedCh:
			// Flush any terminal event accepted concurrently before shutdown.
			if drainAndApply(&cursor) {
				return
			}
			sse.Fail("server_shutdown", "server shutting down")
			return
		}
	}
}

func (b *Broker) enqueue(in ResponsesInput) (*requestRecord, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrBrokerClosed
	}
	b.pruneLocked(time.Now().UTC())
	if b.countPendingLocked() >= b.opts.MaxPendingQueue {
		return nil, ErrQueueFull
	}

	sess := b.resolveSessionLocked(in)
	sess.Turns++
	sess.UpdatedAt = time.Now().UTC()

	baseCommitted := sess.CommittedMessages
	hasPrevResp := in.PreviousResponseID != ""
	if hasPrevResp {
		if prevReq, ok := b.responses[in.PreviousResponseID]; ok && prevReq.CommittedAfter != nil {
			baseCommitted = prevReq.CommittedAfter
		}
	}
	fullMsgs, newMsgs, newIndices := stitchConversationMessages(baseCommitted, in.Messages, hasPrevResp)
	allowed := indexAllowedTools(in.Tools)
	images := collectImages(fullMsgs)

	b.reqSeq++
	reqID := fmt.Sprintf("req_%d_%s", b.reqSeq, randomHex(3))
	respID := fmt.Sprintf("resp_%d_%s", b.reqSeq, randomHex(4))

	req := &requestRecord{
		ID:                 reqID,
		ResponseID:         respID,
		SessionID:          sess.SessionID,
		ConversationKey:    sess.ConversationKey,
		Model:              in.Model,
		Status:             StatusQueued,
		Turn:               sess.Turns,
		CreatedAt:          time.Now().UTC(),
		PreviousResponseID: in.PreviousResponseID,
		SystemPrompt:       in.Instructions,
		SystemHash:         hashText(in.Instructions),
		Tools:              slices.Clone(in.Tools),
		ToolsHash:          hashTools(in.Tools),
		ToolHashes:         hashToolsMap(in.Tools),
		AllowedTools:       allowed,
		ToolChoice:         in.ToolChoice,
		TextFormat:         in.TextFormat,
		FullMessages:       fullMsgs,
		NewMessages:        newMsgs,
		NewIndices:         newIndices,
		Images:             images,
		seenResponseIDs:    make(map[string]struct{}),
		streamNotify:       make(chan struct{}),
		doneCh:             make(chan struct{}),
	}

	b.queue = append(b.queue, req)
	b.requests[req.ID] = req
	b.responses[req.ResponseID] = req
	// in.Model 已由 ParseResponsesRequest 归一化（TrimSpace + 空值回退 manual）。
	b.recordModelLocked(in.Model, req.CreatedAt)
	b.notifyWaitersLocked()
	return req, nil
}

// recordModelLocked 登记一次真实观测到的模型名。调用方必须已持有 b.mu。
// 注册表有界：超出 MaxTrackedModels 时淘汰 lastSeen 最早的那一个。
func (b *Broker) recordModelLocked(model string, now time.Time) {
	name := strings.TrimSpace(model)
	if name == "" {
		return
	}
	if len(name) > MaxModelNameBytes {
		name = truncateUTF8Bytes(name, MaxModelNameBytes)
	}
	if b.models == nil {
		b.models = make(map[string]*knownModel)
	}
	if entry, ok := b.models[name]; ok {
		entry.requests++
		entry.lastSeen = now
		return
	}
	for len(b.models) >= MaxTrackedModels {
		var evictKey string
		evictSeen := time.Time{}
		first := true
		for key, entry := range b.models {
			if first || entry.lastSeen.Before(evictSeen) {
				evictKey, evictSeen, first = key, entry.lastSeen, false
			}
		}
		delete(b.models, evictKey)
	}
	b.models[name] = &knownModel{id: name, requests: 1, created: now, lastSeen: now}
}

// truncateUTF8Bytes 按 UTF-8 边界截断到上限字节数，避免把多字节字符切成半个。
func truncateUTF8Bytes(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if cut == 0 {
		return ""
	}
	return s[:cut]
}

// Models returns observed model names ordered by last_seen descending.
func (b *Broker) Models() []ModelUsage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.modelsLocked().Models
}

// modelsLocked 是 Models 的持锁版本，供 Snapshot 复用，避免二次加锁死锁。
func (b *Broker) modelsLocked() ModelsSnapshot {
	if len(b.models) == 0 {
		return ModelsSnapshot{Models: []ModelUsage{}}
	}
	entries := make([]*knownModel, 0, len(b.models))
	for _, entry := range b.models {
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].lastSeen.Equal(entries[j].lastSeen) {
			return entries[i].id < entries[j].id
		}
		return entries[i].lastSeen.After(entries[j].lastSeen)
	})
	usages := make([]ModelUsage, 0, len(entries))
	for _, entry := range entries {
		usages = append(usages, ModelUsage{
			ID:       entry.id,
			Requests: entry.requests,
			Created:  entry.created.Unix(),
			LastSeen: entry.lastSeen.UTC().Format(time.RFC3339),
		})
	}
	return ModelsSnapshot{Models: usages}
}

func (b *Broker) countPendingLocked() int {
	n := 0
	for _, r := range b.requests {
		if r.Status == StatusQueued || r.Status == StatusClaimed || r.Status == StatusStreaming {
			n++
		}
	}
	return n
}

func (b *Broker) resolveSessionLocked(in ResponsesInput) *sessionState {
	if in.PreviousResponseID != "" {
		if prevReq, ok := b.responses[in.PreviousResponseID]; ok {
			if sess, ok := b.sessions[prevReq.SessionID]; ok {
				if in.ConversationKey != "" && sess.ConversationKey == "" {
					sess.ConversationKey = in.ConversationKey
					b.convSessions[in.ConversationKey] = sess
				}
				return sess
			}
		}
	}
	if in.ConversationKey != "" {
		if sess, ok := b.convSessions[in.ConversationKey]; ok {
			return sess
		}
	}
	b.sessSeq++
	sessID := fmt.Sprintf("s_%d_%s", b.sessSeq, randomHex(3))
	sess := &sessionState{
		SessionID:       sessID,
		ConversationKey: in.ConversationKey,
		UpdatedAt:       time.Now().UTC(),
	}
	b.sessions[sessID] = sess
	if in.ConversationKey != "" {
		b.convSessions[in.ConversationKey] = sess
	}
	return sess
}

func seqIndices(start, count int) []int {
	if count <= 0 {
		return nil
	}
	out := make([]int, count)
	for i := 0; i < count; i++ {
		out[i] = start + i
	}
	return out
}

func stitchConversationMessages(committed []Message, incoming []Message, hasPreviousResponseID bool) (full []Message, delta []Message, deltaIndices []int) {
	if len(committed) == 0 {
		copied := slices.Clone(incoming)
		return copied, copied, seqIndices(0, len(copied))
	}
	if hasPreviousResponseID {
		prefix := commonPrefixLen(committed, incoming)
		if prefix > 0 {
			newPart := slices.Clone(incoming[prefix:])
			all := make([]Message, 0, len(committed)+len(newPart))
			all = append(all, committed...)
			all = append(all, newPart...)
			return all, newPart, seqIndices(len(committed), len(newPart))
		}
		all := make([]Message, 0, len(committed)+len(incoming))
		all = append(all, committed...)
		all = append(all, incoming...)
		return all, slices.Clone(incoming), seqIndices(len(committed), len(incoming))
	}
	// Stateless callers replay full transcript each turn via conversation or prompt_cache_key.
	prefix := commonPrefixLen(committed, incoming)
	if prefix == len(committed) && prefix <= len(incoming) {
		return slices.Clone(incoming), slices.Clone(incoming[prefix:]), seqIndices(prefix, len(incoming)-prefix)
	}
	// If leading context (e.g. dynamic developer metadata) changed or early turns
	// compacted, anchor on the longest matching suffix (>=2 msgs) of committed so
	// unchanged history stays omitted with accurate message indices.
	if anchorStart, anchorLen := findCommittedTailAnchor(committed[prefix:], incoming[prefix:]); anchorLen >= 2 {
		absStart := prefix + anchorStart
		absEnd := absStart + anchorLen
		deltaLen := (absStart - prefix) + (len(incoming) - absEnd)
		delta = make([]Message, 0, deltaLen)
		deltaIndices = make([]int, 0, deltaLen)
		for i := prefix; i < absStart; i++ {
			delta = append(delta, incoming[i])
			deltaIndices = append(deltaIndices, i)
		}
		for i := absEnd; i < len(incoming); i++ {
			delta = append(delta, incoming[i])
			deltaIndices = append(deltaIndices, i)
		}
		return slices.Clone(incoming), delta, deltaIndices
	}
	// Fallback when client sent partial overlapping prefix after pruning/truncation.
	if prefix > 0 && prefix <= len(incoming) {
		return slices.Clone(incoming), slices.Clone(incoming[prefix:]), seqIndices(prefix, len(incoming)-prefix)
	}
	copied := slices.Clone(incoming)
	return copied, copied, seqIndices(0, len(copied))
}

func findCommittedTailAnchor(committedRem, incomingRem []Message) (anchorStart, anchorLen int) {
	if len(committedRem) < 2 || len(incomingRem) < 2 {
		return -1, 0
	}
	maxLen := min(len(committedRem), len(incomingRem))
	for length := maxLen; length >= 2; length-- {
		tail := committedRem[len(committedRem)-length:]
		for pos := 0; pos <= len(incomingRem)-length; pos++ {
			if messageSlicesEqual(tail, incomingRem[pos:pos+length]) {
				return pos, length
			}
		}
	}
	return -1, 0
}

func messageSlicesEqual(a, b []Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !messagesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func commonPrefixLen(a, b []Message) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if !messagesEqual(a[i], b[i]) {
			return i
		}
	}
	return n
}

func messagesEqual(a, b Message) bool {
	if a.Role != b.Role || a.Kind != b.Kind || a.Name != b.Name || a.CallID != b.CallID {
		return false
	}
	if a.Text == b.Text {
		return true
	}
	// Tolerate canonical whitespace differences when a client replays tool_call JSON.
	if a.Kind == "tool_call" && json.Valid([]byte(a.Text)) && json.Valid([]byte(b.Text)) {
		return compactJSONString(a.Text) == compactJSONString(b.Text)
	}
	return false
}

func compactJSONString(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(raw)
}

func indexAllowedTools(tools []ToolDef) map[string]ToolDef {
	out := make(map[string]ToolDef, len(tools)*2)
	for _, t := range tools {
		if t.Namespace != "" {
			out[t.Namespace+"."+t.Name] = t
		}
		if _, exists := out[t.Name]; !exists {
			out[t.Name] = t
		}
	}
	return out
}

func collectImages(msgs []Message) map[string]ImageRef {
	var out map[string]ImageRef
	for _, msg := range msgs {
		for _, img := range msg.Images {
			if out == nil {
				out = make(map[string]ImageRef)
			}
			out[img.Ref] = img
		}
	}
	return out
}

func (b *Broker) drainStream(req *requestRecord, cursor *int) ([]streamDelivery, *streamDelivery, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if *cursor < len(req.streamQueue) {
		batch := slices.Clone(req.streamQueue[*cursor:])
		*cursor = len(req.streamQueue)
		return batch, nil, req.streamNotify
	}
	switch req.Status {
	case StatusCompleted:
		return nil, &streamDelivery{terminate: true, reason: "stop"}, req.streamNotify
	case StatusFailed, StatusCancelled:
		msg := req.ErrorMessage
		if msg == "" {
			msg = string(req.Status)
		}
		return nil, &streamDelivery{terminate: true, errCode: string(req.Status), errMsg: msg}, req.streamNotify
	default:
		return nil, nil, req.streamNotify
	}
}

func (b *Broker) cancelOnDisconnect(requestID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	req, ok := b.requests[requestID]
	if !ok {
		return
	}
	if req.Status == StatusQueued || req.Status == StatusClaimed || req.Status == StatusStreaming {
		req.Status = StatusCancelled
		req.ErrorMessage = "upstream HTTP client disconnected"
		req.CompletedAt = time.Now().UTC()
		req.pushTerminateLocked("client_disconnected", req.ErrorMessage)
		closeDoneOnce(&req.doneCh)
		b.notifyWaitersLocked()
	}
}

// CancelRequest aborts an active or queued manual request from the Web Dashboard.
func (b *Broker) CancelRequest(requestID, reason string) (QueueEntryView, error) {
	id := strings.TrimSpace(requestID)
	if id == "" {
		return QueueEntryView{}, fmt.Errorf("request_id is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "cancelled from dashboard"
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	req, ok := b.requests[id]
	if !ok {
		return QueueEntryView{}, fmt.Errorf("request %q not found", id)
	}
	if req.Status != StatusQueued && req.Status != StatusClaimed && req.Status != StatusStreaming {
		return QueueEntryView{}, fmt.Errorf("request %q is already %s", id, req.Status)
	}
	req.Status = StatusCancelled
	req.ErrorMessage = reason
	req.CompletedAt = time.Now().UTC()
	req.pushTerminateLocked("cancelled", reason)
	closeDoneOnce(&req.doneCh)
	b.notifyWaitersLocked()
	return b.toEntryViewLocked(req, b.opts), nil
}

// Wait implements the progressive llm_wait MCP tool (wait, history, prompt).
func (b *Broker) Wait(ctx context.Context, in WaitInput) (WaitOutput, []ImageRef, error) {
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action == "" {
		action = "wait"
	}
	switch action {
	case "wait":
		return b.waitForRequest(ctx, in)
	case "history":
		return b.readHistory(in)
	case "prompt":
		return b.readPrompt(in)
	default:
		return WaitOutput{}, nil, fmt.Errorf("unsupported llm_wait action %q (expected wait, history, or prompt)", action)
	}
}

func (b *Broker) waitForRequest(ctx context.Context, in WaitInput) (WaitOutput, []ImageRef, error) {
	opts := b.Options()
	waitMs := opts.DefaultWaitMs
	if in.WaitMs != nil {
		waitMs = clampInt(*in.WaitMs, 0, opts.MaxWaitMs)
	}
	callerKey := strings.TrimSpace(in.CallerKey)
	targetReqID := strings.TrimSpace(in.RequestID)
	targetSessID := strings.TrimSpace(in.SessionID)
	if targetSessID != "" {
		b.mu.Lock()
		b.sessionWaiters[targetSessID]++
		b.mu.Unlock()
		defer func() {
			b.mu.Lock()
			if b.sessionWaiters[targetSessID] > 1 {
				b.sessionWaiters[targetSessID]--
			} else {
				delete(b.sessionWaiters, targetSessID)
			}
			b.mu.Unlock()
		}()
	}

	deadline := time.Now().Add(time.Duration(waitMs) * time.Millisecond)

	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return WaitOutput{}, nil, ErrBrokerClosed
		}
		b.pruneLocked(time.Now().UTC())

		req, err := b.claimQueuedLocked(targetReqID, targetSessID)
		if err != nil {
			b.mu.Unlock()
			return WaitOutput{}, nil, err
		}
		if req != nil {
			if callerKey != "" {
				req.ClaimedCallerKey = callerKey
			}
			turn, imgs := b.buildClaimedTurnLocked(req, in.Fresh)
			b.mu.Unlock()
			return WaitOutput{
				Status: "claimed",
				Action: "wait",
				Turn:   &turn,
			}, imgs, nil
		}

		pendingCount := b.countQueuedLocked()
		notifyCh := b.waitNotify
		b.mu.Unlock()

		remaining := time.Until(deadline)
		if waitMs == 0 || remaining <= 0 {
			return WaitOutput{
				Status:       "idle",
				Action:       "wait",
				PendingCount: pendingCount,
				Hint:         "Queue idle; call llm_wait again to continue waiting.",
			}, nil, nil
		}

		timer := time.NewTimer(remaining)
		select {
		case <-notifyCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			b.mu.Lock()
			pendingCount = b.countQueuedLocked()
			b.mu.Unlock()
			return WaitOutput{
				Status:       "idle",
				Action:       "wait",
				PendingCount: pendingCount,
				Hint:         "Queue idle; call llm_wait again to continue waiting.",
			}, nil, nil
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return WaitOutput{}, nil, ctx.Err()
		case <-b.closedCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return WaitOutput{}, nil, ErrBrokerClosed
		}
	}
}

func (b *Broker) claimQueuedLocked(requestID, sessionID string) (*requestRecord, error) {
	if requestID != "" {
		req, ok := b.requests[requestID]
		if !ok {
			return nil, fmt.Errorf("request %q not found", requestID)
		}
		if sessionID != "" && req.SessionID != sessionID {
			return nil, fmt.Errorf("request %q belongs to session %q, not %q", requestID, req.SessionID, sessionID)
		}
		if req.Status != StatusQueued {
			return nil, fmt.Errorf("request %q is %s and cannot be claimed", requestID, req.Status)
		}
		req.Status = StatusClaimed
		req.ClaimedAt = time.Now().UTC()
		b.notifyWaitersLocked()
		return req, nil
	}

	for _, req := range b.queue {
		if req.Status != StatusQueued {
			continue
		}
		if sessionID != "" && req.SessionID != sessionID {
			continue
		}
		// Never let an un-scoped generic poller hijack a follow-up turn when that session already has an active waiter.
		if sessionID == "" && req.Turn > 1 && b.sessionWaiters[req.SessionID] > 0 {
			continue
		}
		req.Status = StatusClaimed
		req.ClaimedAt = time.Now().UTC()
		b.notifyWaitersLocked()
		return req, nil
	}
	return nil, nil
}

func (b *Broker) countQueuedLocked() int {
	n := 0
	for _, req := range b.requests {
		if req.Status == StatusQueued {
			n++
		}
	}
	return n
}

func (b *Broker) buildClaimedTurnLocked(req *requestRecord, forceFresh bool) (ClaimedTurn, []ImageRef) {
	sess := b.sessions[req.SessionID]
	startIdx := max(0, len(req.FullMessages)-len(req.NewMessages))
	compactMsgs := make([]CompactMessage, 0, len(req.NewMessages))
	var imgs []ImageRef
	truncatedItems := 0

	for i, msg := range req.NewMessages {
		msgIdx := startIdx + i
		if i < len(req.NewIndices) {
			msgIdx = req.NewIndices[i]
		}
		cm := toCompactMessage(msgIdx, msg, b.opts.PreviewBytes)
		if cm.Truncated {
			truncatedItems++
		}
		compactMsgs = append(compactMsgs, cm)
		imgs = append(imgs, msg.Images...)
	}

	toolNames := canonicalToolNames(req.Tools)
	callerChanged := req.ClaimedCallerKey != "" && sess != nil && sess.ConfirmedCallerKey != "" && sess.ConfirmedCallerKey != req.ClaimedCallerKey
	hasConfirmed := sess != nil && sess.ConfirmedGuide
	cacheReset := hasConfirmed && (forceFresh || callerChanged)

	var (
		sysChanged   bool
		toolsChanged bool
		changedTools []string
		removedTools []string
	)
	if !hasConfirmed || cacheReset {
		sysChanged = req.SystemPrompt != ""
		toolsChanged = len(req.Tools) > 0
	} else {
		sysChanged = req.SystemPrompt != "" && sess.ConfirmedSystemHash != req.SystemHash
		if req.ToolsHash != sess.ConfirmedToolsHash {
			if len(sess.ConfirmedToolHashes) > 0 {
				changedTools, removedTools = diffToolHashes(toolNames, req.ToolHashes, sess.ConfirmedToolHashes)
				toolsChanged = len(changedTools) > 0
				if len(changedTools) == len(toolNames) {
					changedTools = nil
				}
			} else if len(req.Tools) > 0 {
				toolsChanged = true
			}
		}
	}

	omittedCount := max(0, len(req.FullMessages)-len(req.NewMessages))
	hasPromptData := req.SystemPrompt != "" || len(req.Tools) > 0
	hasHistoryData := omittedCount > 0 || truncatedItems > 0

	var guide string
	if !hasConfirmed || cacheReset {
		guide = buildTurnGuide(hasPromptData, hasHistoryData, b.opts.DefaultWaitMs)
	}

	return ClaimedTurn{
		RequestID:          req.ID,
		ResponseID:         req.ResponseID,
		SessionID:          req.SessionID,
		Turn:               req.Turn,
		Model:              req.Model,
		PreviousResponseID: req.PreviousResponseID,
		NewMessages:        compactMsgs,
		Context: ContextManifest{
			TotalMessages:       len(req.FullMessages),
			OmittedMessages:     omittedCount,
			HasSystemPrompt:     req.SystemPrompt != "",
			SystemPromptBytes:   len(req.SystemPrompt),
			SystemPromptChanged: sysChanged,
			ToolCount:           len(req.Tools),
			ToolNames:           toolNames,
			ToolsChanged:        toolsChanged,
			ChangedTools:        changedTools,
			RemovedTools:        removedTools,
			CacheReset:          cacheReset,
			ToolChoice:          req.ToolChoice,
			TextFormat:          req.TextFormat,
			TruncatedItems:      truncatedItems,
		},
		Guide: guide,
	}, imgs
}

func diffToolHashes(orderedNames []string, current, confirmed map[string]string) (changed, removed []string) {
	for _, name := range orderedNames {
		if prevHash, ok := confirmed[name]; !ok || prevHash != current[name] {
			changed = append(changed, name)
		}
	}
	for name := range confirmed {
		if _, ok := current[name]; !ok {
			removed = append(removed, name)
		}
	}
	if len(removed) > 1 {
		sort.Strings(removed)
	}
	return changed, removed
}

func cloneStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return maps.Clone(m)
}

func buildTurnGuide(hasPrompt, hasHistory bool, defaultWaitMs int) string {
	if defaultWaitMs <= 0 {
		defaultWaitMs = DefaultWaitTimeoutMs
	}
	parts := make([]string, 0, 3)
	if hasPrompt {
		parts = append(parts, `Fetch system_prompt/tool schemas on demand: llm_wait(action="prompt", request_id, include=["system_prompt","tools"], tool_names=[...]).`)
	}
	if hasHistory {
		parts = append(parts, `Fetch clipped/earlier text: llm_wait(action="history", request_id, call_id|cursor).`)
	}
	parts = append(parts, fmt.Sprintf(`Reply via llm_submit(request_id, events=[...], wait_ms=%d); events: thinking{text}, text{text}, tool_call{name,arguments,call_id?}, done{reason:stop|tool_calls}, error{text}.`, defaultWaitMs))
	return strings.Join(parts, " ")
}

func displayToolName(t ToolDef) string {
	if t.Namespace != "" {
		return t.Namespace + "." + t.Name
	}
	return t.Name
}

func toCompactMessage(index int, msg Message, previewBytes int) CompactMessage {
	preview, totalBytes, truncated := CompactPreview(msg.Text, previewBytes)
	var imgRefs []string
	if len(msg.Images) > 0 {
		imgRefs = make([]string, 0, len(msg.Images))
		for _, img := range msg.Images {
			imgRefs = append(imgRefs, img.Ref)
		}
	}
	return CompactMessage{
		Index:     index,
		Role:      msg.Role,
		Kind:      msg.Kind,
		Name:      msg.Name,
		CallID:    msg.CallID,
		Preview:   preview,
		TextBytes: totalBytes,
		Truncated: truncated,
		ImageRefs: imgRefs,
	}
}

func (b *Broker) readHistory(in WaitInput) (WaitOutput, []ImageRef, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	opts := b.opts
	req, err := b.resolveReadableRequestLocked(in.RequestID, in.SessionID)
	if err != nil {
		return WaitOutput{}, nil, err
	}

	callID := strings.TrimSpace(in.CallID)
	wantSlice := callID != "" || (in.Cursor != nil && (in.OffsetBytes > 0 || in.MaxBytes > 0))
	if wantSlice {
		idx, msg, ok := findTargetMessage(req.FullMessages, callID, in.Cursor)
		if !ok {
			return WaitOutput{}, nil, fmt.Errorf("target message not found in request %q", req.ID)
		}
		maxBytes := clampSliceBytesWithOptions(in.MaxBytes, opts.SliceBytes, opts.MaxSliceBytes)
		chunk, startB, endB, totalB, eof := SliceUTF8Bytes(msg.Text, in.OffsetBytes, maxBytes)
		return WaitOutput{
			Status: "ok",
			Action: "history",
			Slice: &SliceResult{
				Target:     "message",
				Index:      &idx,
				CallID:     msg.CallID,
				Role:       msg.Role,
				Name:       msg.Name,
				Content:    chunk,
				StartByte:  startB,
				EndByte:    endB,
				NextOffset: endB,
				TotalBytes: totalB,
				EOF:        eof,
			},
		}, slices.Clone(msg.Images), nil
	}

	total := len(req.FullMessages)
	limit := clampInt(in.Limit, 1, opts.MaxHistoryLimit)
	if in.Limit <= 0 {
		limit = opts.HistoryLimit
	}
	start := 0
	if in.Cursor != nil {
		start = clampInt(*in.Cursor, 0, total)
	}
	end := min(total, start+limit)

	items := make([]CompactMessage, 0, max(0, end-start))
	var imgs []ImageRef
	for i := start; i < end; i++ {
		items = append(items, toCompactMessage(i, req.FullMessages[i], opts.PreviewBytes))
		imgs = append(imgs, req.FullMessages[i].Images...)
	}
	var nextCursor *int
	if end < total {
		n := end
		nextCursor = &n
	}
	return WaitOutput{
		Status:     "ok",
		Action:     "history",
		History:    items,
		NextCursor: nextCursor,
		TotalItems: total,
	}, imgs, nil
}

func findTargetMessage(msgs []Message, callID string, cursor *int) (int, Message, bool) {
	if callID != "" {
		// Prefer tool_output / custom_tool_call_output because call and output share call_id.
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].CallID == callID && (msgs[i].Kind == "tool_output" || msgs[i].Kind == "custom_tool_call_output") {
				return i, msgs[i], true
			}
		}
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].CallID == callID {
				return i, msgs[i], true
			}
		}
		return -1, Message{}, false
	}
	if cursor != nil && *cursor >= 0 && *cursor < len(msgs) {
		return *cursor, msgs[*cursor], true
	}
	return -1, Message{}, false
}

func (b *Broker) readPrompt(in WaitInput) (WaitOutput, []ImageRef, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	opts := b.opts
	req, err := b.resolveReadableRequestLocked(in.RequestID, in.SessionID)
	if err != nil {
		return WaitOutput{}, nil, err
	}

	includes := make(map[string]bool)
	if len(in.Include) == 0 {
		includes["system_prompt"] = true
		includes["tools"] = true
	} else {
		for _, inc := range in.Include {
			includes[strings.ToLower(strings.TrimSpace(inc))] = true
		}
	}

	payload := &PromptPayload{}
	if includes["system_prompt"] {
		chunk, startB, endB, totalB, eof := SliceUTF8Bytes(req.SystemPrompt, in.OffsetBytes, clampSliceBytesWithOptions(in.MaxBytes, opts.SliceBytes, opts.MaxSliceBytes))
		payload.SystemPrompt = &SliceResult{
			Target:     "system_prompt",
			Content:    chunk,
			StartByte:  startB,
			EndByte:    endB,
			NextOffset: endB,
			TotalBytes: totalB,
			EOF:        eof,
		}
	}

	if includes["tools"] {
		filtered := filterTools(req.Tools, in.ToolNames)
		totalTools := len(filtered)
		start := 0
		if in.Cursor != nil {
			start = clampInt(*in.Cursor, 0, totalTools)
		}
		limit := totalTools
		if in.Limit > 0 {
			limit = min(in.Limit, opts.MaxHistoryLimit)
		} else if totalTools > opts.HistoryLimit && len(in.ToolNames) == 0 {
			limit = opts.HistoryLimit
		}
		end := min(totalTools, start+limit)
		tools := make([]ToolSummary, 0, max(0, end-start))
		for _, t := range filtered[start:end] {
			tools = append(tools, ToolSummary{
				Type:        t.Type,
				Name:        t.Name,
				Namespace:   t.Namespace,
				Description: t.Description,
				Parameters:  t.Parameters,
				Format:      t.Format,
				Strict:      t.Strict,
			})
		}
		payload.Tools = tools
		payload.TotalTools = totalTools
		if end < totalTools {
			n := end
			payload.NextToolCursor = &n
		}
	}

	if includes["guide"] {
		payload.Guide = buildTurnGuide(true, true, opts.DefaultWaitMs)
	}

	return WaitOutput{
		Status: "ok",
		Action: "prompt",
		Prompt: payload,
	}, nil, nil
}

func filterTools(tools []ToolDef, names []string) []ToolDef {
	if len(names) == 0 {
		return tools
	}
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[strings.TrimSpace(n)] = struct{}{}
	}
	var out []ToolDef
	for _, t := range tools {
		if _, ok := want[t.Name]; ok {
			out = append(out, t)
			continue
		}
		if t.Namespace != "" {
			if _, ok := want[t.Namespace+"."+t.Name]; ok {
				out = append(out, t)
			}
		}
	}
	return out
}

func (b *Broker) resolveReadableRequestLocked(requestID, sessionID string) (*requestRecord, error) {
	reqID := strings.TrimSpace(requestID)
	sessID := strings.TrimSpace(sessionID)
	if reqID != "" {
		req, ok := b.requests[reqID]
		if !ok {
			return nil, fmt.Errorf("request %q not found", reqID)
		}
		if sessID != "" && req.SessionID != sessID {
			return nil, fmt.Errorf("request %q belongs to session %q, not %q", reqID, req.SessionID, sessID)
		}
		return req, nil
	}
	if sessID != "" {
		// Return the most recent request for the requested session.
		for i := len(b.queue) - 1; i >= 0; i-- {
			if b.queue[i].SessionID == sessID {
				return b.queue[i], nil
			}
		}
		return nil, fmt.Errorf("no requests found for session %q", sessID)
	}
	// Fallback to the latest claimed/streaming request if only one operator is active.
	for i := len(b.queue) - 1; i >= 0; i-- {
		if b.queue[i].Status == StatusClaimed || b.queue[i].Status == StatusStreaming {
			return b.queue[i], nil
		}
	}
	return nil, fmt.Errorf("request_id or session_id is required")
}

// Submit applies a batch of events from llm_submit to the live SSE stream and
// optionally waits on a terminal done event for the follow-up turn.
func (b *Broker) Submit(ctx context.Context, in SubmitInput) (SubmitOutput, []ImageRef, error) {
	if len(in.Events) == 0 {
		return SubmitOutput{}, nil, fmt.Errorf("events must not be empty")
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return SubmitOutput{}, nil, ErrBrokerClosed
	}
	req, err := b.resolveSubmitRequestLocked(in.RequestID, in.SessionID)
	if err != nil {
		b.mu.Unlock()
		return SubmitOutput{}, nil, err
	}
	respKey := strings.TrimSpace(in.ResponseID)
	if respKey != "" {
		if _, dup := req.seenResponseIDs[respKey]; dup {
			out := SubmitOutput{
				Status:    "duplicate",
				RequestID: req.ID,
				SessionID: req.SessionID,
			}
			b.mu.Unlock()
			return out, nil, nil
		}
	}

	prepared, terminal, err := validateAndPrepareEvents(req, in.Events)
	if err != nil {
		b.mu.Unlock()
		return SubmitOutput{}, nil, err
	}

	if req.Status == StatusQueued {
		req.ClaimedAt = time.Now().UTC()
	}
	req.Status = StatusStreaming
	if respKey != "" {
		req.seenResponseIDs[respKey] = struct{}{}
	}

	var callIDs []string
	var errMsg string
	doneReason := ""
	for _, ev := range prepared {
		switch ev.Type {
		case "thinking":
			flushPendingAssistantText(req)
			req.streamQueue = append(req.streamQueue, streamDelivery{event: &ev})
		case "text":
			delta := ev.Text
			if delta == "" {
				delta = ev.Delta
			}
			req.pendingText.WriteString(delta)
			req.streamQueue = append(req.streamQueue, streamDelivery{event: &ev})
		case "tool_call":
			flushPendingAssistantText(req)
			callIDs = append(callIDs, ev.CallID)
			req.emittedCalls++
			kind := "tool_call"
			payloadText := formatFunctionArguments(ev)
			if def, ok := req.AllowedTools[ev.Name]; ok && def.Type == "custom" {
				kind = "custom_tool_call"
				payloadText = formatCustomToolInput(ev)
			}
			req.emittedMessages = append(req.emittedMessages, Message{
				Role:   "assistant",
				Kind:   kind,
				Name:   ev.Name,
				CallID: ev.CallID,
				Text:   payloadText,
			})
			req.streamQueue = append(req.streamQueue, streamDelivery{event: &ev})
		case "done":
			flushPendingAssistantText(req)
			doneReason = strings.TrimSpace(ev.Reason)
			if doneReason == "" {
				doneReason = "stop"
			}
			req.streamQueue = append(req.streamQueue, streamDelivery{event: &ev})
		case "error":
			flushPendingAssistantText(req)
			errMsg = firstNonEmptyTrimmed(ev.Error, ev.Text, ev.Delta, ev.Reason, "manual LLM returned an error")
			req.streamQueue = append(req.streamQueue, streamDelivery{event: &ev})
		}
	}

	req.notifyStreamLocked()
	statusOut := string(req.Status)
	sessID := req.SessionID

	if terminal == "done" {
		committed := make([]Message, 0, len(req.FullMessages)+len(req.emittedMessages))
		committed = append(committed, req.FullMessages...)
		committed = append(committed, req.emittedMessages...)
		req.CommittedAfter = committed
		if sess := b.sessions[req.SessionID]; sess != nil {
			sess.CommittedMessages = committed
			sess.LastResponseID = req.ResponseID
			sess.ConfirmedSystemHash = req.SystemHash
			sess.ConfirmedToolsHash = req.ToolsHash
			sess.ConfirmedToolHashes = cloneStringMap(req.ToolHashes)
			sess.ConfirmedGuide = true
			if req.ClaimedCallerKey != "" {
				sess.ConfirmedCallerKey = req.ClaimedCallerKey
			}
			sess.UpdatedAt = time.Now().UTC()
		}
		req.Status = StatusCompleted
		req.CompletedAt = time.Now().UTC()
		closeDoneOnce(&req.doneCh)
		b.notifyWaitersLocked()
		statusOut = "completed"
	} else if terminal == "error" {
		req.Status = StatusFailed
		req.ErrorMessage = errMsg
		req.CompletedAt = time.Now().UTC()
		closeDoneOnce(&req.doneCh)
		b.notifyWaitersLocked()
		statusOut = "failed"
	}

	out := SubmitOutput{
		Status:    statusOut,
		RequestID: req.ID,
		SessionID: sessID,
		Applied:   len(prepared),
		ToolCalls: callIDs,
	}
	b.mu.Unlock()

	if terminal == "done" && in.WaitMs != nil && *in.WaitMs > 0 {
		waitRes, imgs, waitErr := b.waitForRequest(ctx, WaitInput{
			Action:    "wait",
			SessionID: sessID,
			CallerKey: in.CallerKey,
			WaitMs:    in.WaitMs,
		})
		if waitErr == nil && waitRes.Turn != nil {
			out.NextRequest = waitRes.Turn
			return out, imgs, nil
		}
		if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
			return out, nil, nil
		}
	}
	return out, nil, nil
}

func (b *Broker) resolveSubmitRequestLocked(requestID, sessionID string) (*requestRecord, error) {
	reqID := strings.TrimSpace(requestID)
	sessID := strings.TrimSpace(sessionID)
	if reqID != "" {
		req, ok := b.requests[reqID]
		if !ok {
			return nil, fmt.Errorf("request %q not found", reqID)
		}
		if sessID != "" && req.SessionID != sessID {
			return nil, fmt.Errorf("request %q belongs to session %q, not %q", reqID, req.SessionID, sessID)
		}
		if req.Status != StatusQueued && req.Status != StatusClaimed && req.Status != StatusStreaming {
			return nil, fmt.Errorf("request %q is %s and cannot accept events", reqID, req.Status)
		}
		return req, nil
	}
	if sessID != "" {
		for i := len(b.queue) - 1; i >= 0; i-- {
			r := b.queue[i]
			if r.SessionID == sessID && (r.Status == StatusClaimed || r.Status == StatusStreaming) {
				return r, nil
			}
		}
		return nil, fmt.Errorf("no active request found for session %q", sessID)
	}
	return nil, fmt.Errorf("request_id or session_id is required")
}

func flushPendingAssistantText(req *requestRecord) {
	if req.pendingText.Len() == 0 {
		return
	}
	req.emittedMessages = append(req.emittedMessages, Message{
		Role: "assistant",
		Kind: "message",
		Text: req.pendingText.String(),
	})
	req.pendingText.Reset()
}

func validateAndPrepareEvents(req *requestRecord, events []Event) ([]Event, string, error) {
	prepared := make([]Event, 0, len(events))
	terminal := ""
	nextCallSeq := req.callSeq
	totalCalls := req.emittedCalls

	for i, ev := range events {
		if terminal != "" {
			return nil, "", fmt.Errorf("event %d (%s) cannot appear after terminal %s event", i, ev.Type, terminal)
		}
		ev.Type = strings.ToLower(strings.TrimSpace(ev.Type))
		switch ev.Type {
		case "thinking", "text":
			prepared = append(prepared, ev)
		case "tool_call":
			name := strings.TrimSpace(ev.Name)
			if name == "" {
				return nil, "", fmt.Errorf("event %d tool_call requires non-empty name", i)
			}
			def, allowed, reason := validateToolCallAllowed(req, name)
			if !allowed {
				return nil, "", fmt.Errorf("event %d tool_call %q rejected: %s", i, name, reason)
			}
			ev.Name = def.Name
			if def.Type == "function" {
				if argStr, ok := ev.Arguments.(string); ok && strings.TrimSpace(argStr) != "" && !json.Valid([]byte(argStr)) {
					return nil, "", fmt.Errorf("event %d tool_call %q arguments must be valid JSON", i, name)
				}
			}
			if strings.TrimSpace(ev.CallID) == "" {
				nextCallSeq++
				ev.CallID = fmt.Sprintf("call_%s_%d", req.ResponseID, nextCallSeq)
			}
			totalCalls++
			prepared = append(prepared, ev)
		case "done":
			if req.ToolChoice != nil {
				if mode, ok := req.ToolChoice.(string); ok && strings.EqualFold(mode, "required") && totalCalls == 0 {
					return nil, "", fmt.Errorf("tool_choice is required: emit at least one tool_call before done")
				}
			}
			terminal = "done"
			prepared = append(prepared, ev)
		case "error":
			terminal = "error"
			prepared = append(prepared, ev)
		default:
			return nil, "", fmt.Errorf("event %d has unsupported type %q (expected thinking, text, tool_call, done, error)", i, ev.Type)
		}
	}

	req.callSeq = nextCallSeq
	return prepared, terminal, nil
}

func validateToolCallAllowed(req *requestRecord, name string) (ToolDef, bool, string) {
	if s, ok := req.ToolChoice.(string); ok && strings.EqualFold(strings.TrimSpace(s), "none") {
		return ToolDef{}, false, "tool_choice is set to none"
	}
	def, exists := req.AllowedTools[name]
	if !exists {
		return ToolDef{}, false, "tool not declared in request tools"
	}
	if obj, ok := req.ToolChoice.(map[string]any); ok {
		tcType, _ := obj["type"].(string)
		switch tcType {
		case "function", "custom":
			forcedName, _ := obj["name"].(string)
			if forcedName != "" && forcedName != def.Name && forcedName != displayToolName(def) {
				return ToolDef{}, false, fmt.Sprintf("tool_choice forces %q", forcedName)
			}
		case "allowed_tools":
			if mode, _ := obj["mode"].(string); strings.EqualFold(mode, "none") {
				return ToolDef{}, false, "tool_choice mode is none"
			}
			if list, ok := obj["tools"].([]any); ok && len(list) > 0 {
				permitted := false
				for _, item := range list {
					if m, ok := item.(map[string]any); ok {
						cand, _ := m["name"].(string)
						if cand == def.Name || cand == displayToolName(def) {
							permitted = true
							break
						}
					}
				}
				if !permitted {
					return ToolDef{}, false, "tool is not in tool_choice.tools allowlist"
				}
			}
		}
	}
	return def, true, ""
}

func isTerminalStatus(s RequestStatus) bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

func (b *Broker) pruneLocked(now time.Time) {
	cutoff := now.Add(-b.opts.CacheTTL)
	afterTTL := make([]*requestRecord, 0, len(b.queue))
	termCount := 0
	for _, r := range b.queue {
		isTerm := isTerminalStatus(r.Status)
		if isTerm && !r.CompletedAt.IsZero() && r.CompletedAt.Before(cutoff) {
			delete(b.requests, r.ID)
			delete(b.responses, r.ResponseID)
			continue
		}
		if isTerm {
			termCount++
		}
		afterTTL = append(afterTTL, r)
	}

	finalQueue := afterTTL
	if termCount > b.opts.CacheMaxResponses {
		finalQueue = make([]*requestRecord, 0, len(afterTTL)-(termCount-b.opts.CacheMaxResponses))
		for _, r := range afterTTL {
			if termCount > b.opts.CacheMaxResponses && isTerminalStatus(r.Status) {
				delete(b.requests, r.ID)
				delete(b.responses, r.ResponseID)
				termCount--
				continue
			}
			finalQueue = append(finalQueue, r)
		}
	}
	b.queue = finalQueue

	// Retain binary image bytes for all active turns and newest terminal turns within budget.
	var (
		keptImgPtrs   map[*byte]struct{}
		usedTermBytes int64
	)
	markImgKept := func(data []byte) {
		if len(data) == 0 {
			return
		}
		if keptImgPtrs == nil {
			keptImgPtrs = make(map[*byte]struct{})
		}
		keptImgPtrs[&data[0]] = struct{}{}
	}
	for _, r := range finalQueue {
		if !isTerminalStatus(r.Status) {
			for _, img := range r.Images {
				markImgKept(img.Data)
			}
		}
	}
	for i := len(finalQueue) - 1; i >= 0; i-- {
		r := finalQueue[i]
		if !isTerminalStatus(r.Status) {
			continue
		}
		reqImgBytes := uniqueImageBytes(r.Images, keptImgPtrs)
		if b.opts.MaxRetainedImageBytes > 0 && usedTermBytes+reqImgBytes <= b.opts.MaxRetainedImageBytes {
			usedTermBytes += reqImgBytes
			for _, img := range r.Images {
				markImgKept(img.Data)
			}
			continue
		}
		stripRequestImages(r, keptImgPtrs)
	}

	for id, sess := range b.sessions {
		stripUnretainedMessageImages(sess.CommittedMessages, keptImgPtrs)
		if sess.UpdatedAt.Before(cutoff) {
			hasActive := false
			for _, r := range b.queue {
				if r.SessionID == id {
					hasActive = true
					break
				}
			}
			if !hasActive {
				delete(b.sessions, id)
				if sess.ConversationKey != "" {
					delete(b.convSessions, sess.ConversationKey)
				}
			}
		}
	}
}

func uniqueImageBytes(imgs map[string]ImageRef, alreadyKept map[*byte]struct{}) int64 {
	var total int64
	var seen map[*byte]struct{}
	for _, img := range imgs {
		if len(img.Data) == 0 {
			continue
		}
		ptr := &img.Data[0]
		if _, ok := alreadyKept[ptr]; ok {
			continue
		}
		if seen == nil {
			seen = make(map[*byte]struct{})
		}
		if _, dup := seen[ptr]; dup {
			continue
		}
		seen[ptr] = struct{}{}
		total += int64(len(img.Data))
	}
	return total
}

func stripRequestImages(r *requestRecord, keptImgPtrs map[*byte]struct{}) {
	for k, img := range r.Images {
		if len(img.Data) > 0 {
			if _, ok := keptImgPtrs[&img.Data[0]]; !ok {
				img.Data = nil
				r.Images[k] = img
			}
		}
	}
	stripUnretainedMessageImages(r.FullMessages, keptImgPtrs)
	stripUnretainedMessageImages(r.NewMessages, keptImgPtrs)
	stripUnretainedMessageImages(r.CommittedAfter, keptImgPtrs)
}

func stripUnretainedMessageImages(msgs []Message, keptImgPtrs map[*byte]struct{}) {
	for i := range msgs {
		for j := range msgs[i].Images {
			data := msgs[i].Images[j].Data
			if len(data) == 0 {
				continue
			}
			if _, ok := keptImgPtrs[&data[0]]; !ok {
				msgs[i].Images[j].Data = nil
			}
		}
	}
}

// Snapshot returns the current manual queue state and single copyable prompt for Web UI.
func (b *Broker) Snapshot(enabled bool, responsesPath, mcpEndpoint string) QueueSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(time.Now().UTC())
	opts := b.opts
	tuning := opts.TuningView()

	var stats QueueStats
	stats.SessionsCount = len(b.sessions)
	views := make([]QueueEntryView, 0, len(b.queue))

	// Show newest active/queued requests first so operators see waiting work immediately.
	ordered := slices.Clone(b.queue)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
	})

	for _, r := range ordered {
		switch r.Status {
		case StatusQueued:
			stats.QueuedCount++
		case StatusClaimed, StatusStreaming:
			stats.ActiveCount++
		case StatusCompleted:
			stats.CompletedCount++
		}
		views = append(views, b.toEntryViewLocked(r, opts))
	}

	return QueueSnapshot{
		Enabled:       enabled,
		ResponsesPath: responsesPath,
		MCPEndpoint:   mcpEndpoint,
		IdlePrompt:    BuildManualPromptWithOptions("", "", opts.DefaultWaitMs, false),
		Tuning:        &tuning,
		Stats:         stats,
		Requests:      views,
		Models:        b.modelsLocked().Models,
	}
}

func (b *Broker) toEntryViewLocked(r *requestRecord, opts Options) QueueEntryView {
	previewText := ""
	for i := len(r.NewMessages) - 1; i >= 0; i-- {
		if strings.TrimSpace(r.NewMessages[i].Text) != "" {
			previewText = r.NewMessages[i].Text
			break
		}
	}
	if previewText == "" && len(r.FullMessages) > 0 {
		previewText = r.FullMessages[len(r.FullMessages)-1].Text
	}
	preview, _, _ := CompactPreview(previewText, 320)

	view := QueueEntryView{
		RequestID:       r.ID,
		ResponseID:      r.ResponseID,
		SessionID:       r.SessionID,
		Model:           r.Model,
		Status:          string(r.Status),
		Turn:            r.Turn,
		CreatedAt:       r.CreatedAt.Format(time.RFC3339),
		MessageCount:    len(r.FullMessages),
		NewMessageCount: len(r.NewMessages),
		ToolCount:       len(r.Tools),
		Preview:         preview,
		CopyPrompt:      BuildManualPromptWithOptions(r.ID, r.SessionID, opts.DefaultWaitMs, r.Turn > 1),
		ErrorMessage:    r.ErrorMessage,
	}
	if !r.ClaimedAt.IsZero() {
		view.ClaimedAt = r.ClaimedAt.Format(time.RFC3339)
	}
	if !r.CompletedAt.IsZero() {
		view.CompletedAt = r.CompletedAt.Format(time.RFC3339)
	}
	return view
}

func clampSliceBytesWithOptions(n, defaultBytes, maxBytes int) int {
	if n <= 0 {
		return defaultBytes
	}
	if n > maxBytes {
		return maxBytes
	}
	return n
}

func clampInt(v, minVal, maxVal int) int {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}
