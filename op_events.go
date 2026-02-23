package platform

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	opEventBootstrap = "op.bootstrap"
	opEventStatus    = "op.status"
	opEventStarted   = "step.started"
	opEventEnded     = "step.ended"
	opEventArtifacts = "step.artifacts"
	opEventCompleted = "op.completed"
	opEventFailed    = "op.failed"
	opEventHeartbeat = "op.heartbeat"

	opStatusRunning = "running"
	opStatusDone    = "done"
	opStatusError   = "error"
	opMessageFailed = "operation failed"
	opMessageDone   = "operation completed"

	opEventSubscriberBuffer = 32
	opTotalStepsFullChain   = 4
	opTotalStepsCIChain     = 2
	opTotalStepsSingle      = 1
	opTotalStepsTransition  = 4
	opProgressMin           = 1
	opProgressMax           = 100
)

type opEventDelivery struct {
	Stage       DeliveryStage `json:"stage,omitempty"`
	Environment string        `json:"environment,omitempty"`
	FromEnv     string        `json:"from_env,omitempty"`
	ToEnv       string        `json:"to_env,omitempty"`
}

type opEventPayload struct {
	EventID         string          `json:"event_id"`
	Sequence        int64           `json:"sequence"`
	OpID            string          `json:"op_id"`
	ProjectID       string          `json:"project_id"`
	Kind            OperationKind   `json:"kind"`
	Status          string          `json:"status"`
	At              time.Time       `json:"at"`
	Worker          string          `json:"worker,omitempty"`
	StepIndex       int             `json:"step_index,omitempty"`
	TotalSteps      int             `json:"total_steps,omitempty"`
	ProgressPercent int             `json:"progress_percent,omitempty"`
	DurationMS      int64           `json:"duration_ms,omitempty"`
	Message         string          `json:"message,omitempty"`
	Error           string          `json:"error,omitempty"`
	Artifacts       []string        `json:"artifacts,omitempty"`
	Delivery        opEventDelivery `json:"delivery"`
	Hint            string          `json:"hint,omitempty"`
}

type opEventRecord struct {
	Name    string
	Payload opEventPayload
}

type opEventStream struct {
	records      []opEventRecord
	subscribers  map[uint64]chan opEventRecord
	nextSequence int64
	terminalAt   time.Time
}

type opEventHub struct {
	mu           sync.Mutex
	historyLimit int
	terminalTTL  time.Duration
	nextSubID    uint64
	streams      map[string]*opEventStream
}

func newOpEventHub(historyLimit int, terminalTTL time.Duration) *opEventHub {
	if historyLimit <= 0 {
		historyLimit = opEventsHistoryLimit
	}
	if terminalTTL <= 0 {
		terminalTTL = opEventsRetention
	}
	return &opEventHub{
		mu:           sync.Mutex{},
		historyLimit: historyLimit,
		terminalTTL:  terminalTTL,
		nextSubID:    0,
		streams:      map[string]*opEventStream{},
	}
}

func (h *opEventHub) publish(eventName string, payload opEventPayload) {
	if h == nil || strings.TrimSpace(payload.OpID) == "" {
		return
	}

	now := time.Now().UTC()
	if payload.At.IsZero() {
		payload.At = now
	}

	h.mu.Lock()
	h.cleanupLocked(now)
	stream := h.streamForLocked(payload.OpID)
	stream.nextSequence++
	payload.Sequence = stream.nextSequence
	payload.EventID = strconv.FormatInt(stream.nextSequence, 10)

	record := opEventRecord{Name: eventName, Payload: payload}
	stream.records = append(stream.records, record)
	if len(stream.records) > h.historyLimit {
		stream.records = append([]opEventRecord(nil), stream.records[len(stream.records)-h.historyLimit:]...)
	}
	if payload.Status == opStatusDone ||
		payload.Status == opStatusError ||
		eventName == opEventCompleted ||
		eventName == opEventFailed {
		stream.terminalAt = now
	}

	subs := make([]chan opEventRecord, 0, len(stream.subscribers))
	for _, sub := range stream.subscribers {
		subs = append(subs, sub)
	}
	h.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub <- record:
		default:
		}
	}
}

func (h *opEventHub) subscribe(
	opID string,
	lastEventID string,
) ([]opEventRecord, <-chan opEventRecord, bool, func()) {
	if h == nil {
		return nil, nil, true, func() {}
	}

	opID = strings.TrimSpace(opID)
	now := time.Now().UTC()

	h.mu.Lock()
	h.cleanupLocked(now)
	stream := h.streamForLocked(opID)

	ch := make(chan opEventRecord, opEventSubscriberBuffer)
	h.nextSubID++
	subID := h.nextSubID
	stream.subscribers[subID] = ch

	replay, needsBootstrap := computeOpEventReplay(stream.records, lastEventID)

	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		streamState, ok := h.streams[opID]
		if !ok {
			return
		}
		sub, ok := streamState.subscribers[subID]
		if !ok {
			return
		}
		delete(streamState.subscribers, subID)
		close(sub)
	}

	return replay, ch, needsBootstrap, unsubscribe
}

func (h *opEventHub) latestSequence(opID string) int64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	stream, ok := h.streams[strings.TrimSpace(opID)]
	if !ok {
		return 0
	}
	return stream.nextSequence
}

func (h *opEventHub) streamForLocked(opID string) *opEventStream {
	stream, ok := h.streams[opID]
	if ok {
		return stream
	}
	stream = &opEventStream{
		records:      []opEventRecord{},
		subscribers:  map[uint64]chan opEventRecord{},
		nextSequence: 0,
		terminalAt:   time.Time{},
	}
	h.streams[opID] = stream
	return stream
}

func (h *opEventHub) cleanupLocked(now time.Time) {
	for opID, stream := range h.streams {
		if stream.terminalAt.IsZero() {
			continue
		}
		if len(stream.subscribers) > 0 {
			continue
		}
		if now.Sub(stream.terminalAt) < h.terminalTTL {
			continue
		}
		delete(h.streams, opID)
	}
}

func opEventRange(records []opEventRecord) (int64, int64) {
	if len(records) == 0 {
		return 0, 0
	}
	return records[0].Payload.Sequence, records[len(records)-1].Payload.Sequence
}

func computeOpEventReplay(
	records []opEventRecord,
	lastEventID string,
) ([]opEventRecord, bool) {
	lastEventID = strings.TrimSpace(lastEventID)
	if lastEventID == "" {
		return nil, true
	}

	lastSeq, ok := parseOpEventSequence(lastEventID)
	if !ok {
		return nil, true
	}
	oldest, newest := opEventRange(records)
	if oldest == 0 && newest == 0 {
		return nil, true
	}
	if lastSeq < oldest-1 || lastSeq > newest {
		return nil, true
	}

	replay := make([]opEventRecord, 0, len(records))
	for _, record := range records {
		if record.Payload.Sequence > lastSeq {
			replay = append(replay, record)
		}
	}
	return replay, false
}

func parseOpEventSequence(raw string) (int64, bool) {
	seq, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || seq < 0 {
		return 0, false
	}
	return seq, true
}

func newOpEventBase(op Operation) opEventPayload {
	return opEventPayload{
		EventID:         "",
		Sequence:        0,
		OpID:            op.ID,
		ProjectID:       op.ProjectID,
		Kind:            op.Kind,
		Status:          strings.TrimSpace(op.Status),
		At:              time.Now().UTC(),
		Worker:          "",
		StepIndex:       0,
		TotalSteps:      opTotalSteps(op.Kind),
		ProgressPercent: opProgressPercent(op),
		DurationMS:      0,
		Message:         "",
		Error:           "",
		Artifacts:       nil,
		Delivery: opEventDelivery{
			Stage:       op.Delivery.Stage,
			Environment: op.Delivery.Environment,
			FromEnv:     op.Delivery.FromEnv,
			ToEnv:       op.Delivery.ToEnv,
		},
		Hint: "",
	}
}

func newOpBootstrapSnapshot(op Operation) opEventPayload {
	payload := newOpEventBase(op)
	payload.At = opEventSnapshotTime(op)

	if len(op.Steps) > 0 {
		latestIdx := len(op.Steps) - 1
		latest := op.Steps[latestIdx]
		payload.Worker = strings.TrimSpace(latest.Worker)
		payload.StepIndex = latestIdx + 1
		payload.Message = strings.TrimSpace(latest.Message)
		payload.Error = strings.TrimSpace(latest.Error)
		payload.Artifacts = boundedOpEventArtifacts(latest.Artifacts)
		if !latest.StartedAt.IsZero() && !latest.EndedAt.IsZero() && latest.EndedAt.After(latest.StartedAt) {
			payload.DurationMS = latest.EndedAt.Sub(latest.StartedAt).Milliseconds()
		}
	}
	if payload.Error == "" {
		payload.Error = strings.TrimSpace(op.Error)
	}
	if payload.Error != "" {
		payload.Hint = opFailureHint(payload.Error)
	}

	switch strings.TrimSpace(payload.Status) {
	case statusMessageQueued:
		if payload.Message == "" {
			payload.Message = "operation accepted and queued"
		}
	case opStatusRunning:
		if payload.Message == "" {
			payload.Message = "operation in progress"
		}
	case opStatusDone:
		if payload.Message == "" {
			payload.Message = opMessageDone
		}
	case opStatusError:
		if payload.Message == "" {
			payload.Message = opMessageFailed
		}
	}
	return payload
}

func opEventSnapshotTime(op Operation) time.Time {
	if !op.Finished.IsZero() {
		return op.Finished.UTC()
	}
	if len(op.Steps) > 0 {
		latest := op.Steps[len(op.Steps)-1]
		if !latest.EndedAt.IsZero() {
			return latest.EndedAt.UTC()
		}
		if !latest.StartedAt.IsZero() {
			return latest.StartedAt.UTC()
		}
	}
	if !op.Requested.IsZero() {
		return op.Requested.UTC()
	}
	return time.Now().UTC()
}

func emitOpBootstrap(h *opEventHub, op Operation, msg string) {
	if h == nil {
		return
	}
	payload := newOpEventBase(op)
	payload.Message = strings.TrimSpace(msg)
	h.publish(opEventBootstrap, payload)
}

func emitOpStatus(h *opEventHub, op Operation, msg string) {
	if h == nil {
		return
	}
	payload := newOpEventBase(op)
	payload.Message = strings.TrimSpace(msg)
	h.publish(opEventStatus, payload)
}

func emitOpStepStarted(h *opEventHub, op Operation, worker string, stepIndex int, msg string) {
	if h == nil {
		return
	}
	payload := newOpEventBase(op)
	payload.Worker = strings.TrimSpace(worker)
	payload.StepIndex = stepIndex
	payload.Message = strings.TrimSpace(msg)
	h.publish(opEventStarted, payload)
}

func emitOpStepEnded(
	h *opEventHub,
	op Operation,
	worker string,
	stepIndex int,
	message string,
	stepErr string,
	artifacts []string,
	startedAt time.Time,
	endedAt time.Time,
) {
	if h == nil {
		return
	}
	payload := newOpEventBase(op)
	payload.Worker = strings.TrimSpace(worker)
	payload.StepIndex = stepIndex
	payload.Message = strings.TrimSpace(message)
	payload.Error = strings.TrimSpace(stepErr)
	if payload.Error != "" {
		payload.Hint = opFailureHint(payload.Error)
	}
	payload.Artifacts = boundedOpEventArtifacts(artifacts)
	if !startedAt.IsZero() && !endedAt.IsZero() && endedAt.After(startedAt) {
		payload.DurationMS = endedAt.Sub(startedAt).Milliseconds()
	}
	h.publish(opEventEnded, payload)

	if len(payload.Artifacts) == 0 {
		return
	}
	artifactPayload := payload
	if artifactPayload.Message == "" {
		artifactPayload.Message = "step produced artifacts"
	}
	h.publish(opEventArtifacts, artifactPayload)
}

func emitOpTerminal(h *opEventHub, op Operation) {
	if h == nil {
		return
	}
	payload := newOpEventBase(op)
	payload.Error = strings.TrimSpace(op.Error)
	if payload.Status == opStatusError {
		if payload.Error != "" {
			payload.Hint = opFailureHint(payload.Error)
		}
		if payload.Message == "" {
			payload.Message = opMessageFailed
		}
		h.publish(opEventFailed, payload)
		return
	}
	if payload.Status == opStatusDone {
		payload.Message = opMessageDone
		h.publish(opEventCompleted, payload)
	}
}

func newOpHeartbeatPayload(base opEventPayload, sequence int64) opEventPayload {
	payload := base
	if sequence < 0 {
		sequence = 0
	}
	payload.EventID = strconv.FormatInt(sequence, 10)
	payload.Sequence = sequence
	payload.At = time.Now().UTC()
	payload.Message = "stream heartbeat"
	payload.Worker = ""
	payload.StepIndex = 0
	payload.DurationMS = 0
	payload.Artifacts = nil
	return payload
}

func boundedOpEventArtifacts(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	limit := opEventArtifactsLimit
	if limit < 1 {
		limit = opEventArtifactsLimit
	}
	if len(in) <= limit {
		return append([]string(nil), in...)
	}
	return append([]string(nil), in[:limit]...)
}

func opTotalSteps(kind OperationKind) int {
	switch kind {
	case OpCreate, OpUpdate, OpDelete:
		return opTotalStepsFullChain
	case OpCI:
		return opTotalStepsCIChain
	case OpDeploy:
		return opTotalStepsSingle
	case OpPromote, OpRelease:
		return opTotalStepsTransition
	default:
		return 0
	}
}

func opProgressPercent(op Operation) int {
	total := opTotalSteps(op.Kind)
	if total <= 0 {
		if op.Status == opStatusDone {
			return opProgressMax
		}
		if op.Status == opStatusError {
			return opProgressMax
		}
		return 0
	}
	done := 0
	for _, step := range op.Steps {
		if !step.EndedAt.IsZero() && strings.TrimSpace(step.Error) == "" {
			done++
		}
	}
	pct := int((float64(done) / float64(total)) * opProgressMax)
	switch strings.TrimSpace(op.Status) {
	case opStatusRunning:
		if pct < opProgressMin {
			return opProgressMin
		}
	case opStatusError:
		if pct < opProgressMin {
			return opProgressMin
		}
	case opStatusDone:
		return opProgressMax
	}
	if pct > opProgressMax {
		return opProgressMax
	}
	if pct < 0 {
		return 0
	}
	return pct
}

func opFailureHint(errMsg string) string {
	msg := strings.ToLower(strings.TrimSpace(errMsg))
	switch {
	case msg == "":
		return "Retry the operation after refreshing project state."
	case strings.Contains(msg, "no build image found"):
		return "Run a build first so there is an image ready to deploy."
	case strings.Contains(msg, "from_env") || strings.Contains(msg, "to_env"):
		return "Verify source and target environments exist and are different."
	case strings.Contains(msg, "timeout"):
		return "The operation timed out. Retry and inspect worker step details."
	case strings.Contains(msg, "not found"):
		return "Refresh project data. The target project or artifact may no longer exist."
	default:
		return "Inspect artifacts and step details, then retry when inputs are corrected."
	}
}
