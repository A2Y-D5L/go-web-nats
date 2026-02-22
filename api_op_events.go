package platform

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func (a *API) handleOpEvents(w http.ResponseWriter, r *http.Request, opID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	op, flusher, ok := a.prepareOpEventStream(w, r, opID)
	if !ok {
		return
	}
	writeOpEventHeaders(w)

	lastEventID := readLastEventID(r)
	replay, live, needsBootstrap, unsubscribe := a.opEvents.subscribe(opID, lastEventID)
	defer unsubscribe()

	lastPayload := newOpBootstrapSnapshot(op)
	lastPayload.Sequence = a.opEvents.latestSequence(opID)
	lastPayload.EventID = strconv.FormatInt(lastPayload.Sequence, 10)

	if !writeInitialOpEvents(w, flusher, needsBootstrap, replay, &lastPayload) {
		return
	}

	a.streamLiveOpEvents(r, w, flusher, live, lastPayload)
}

func (a *API) prepareOpEventStream(
	w http.ResponseWriter,
	r *http.Request,
	opID string,
) (Operation, http.Flusher, bool) {
	if a.store == nil || a.opEvents == nil {
		http.Error(w, "operation events unavailable", http.StatusInternalServerError)
		return Operation{}, nil, false
	}

	op, err := a.store.GetOp(r.Context(), opID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return Operation{}, nil, false
		}
		http.Error(w, "failed to read op", http.StatusInternalServerError)
		return Operation{}, nil, false
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return Operation{}, nil, false
	}
	return op, flusher, true
}

func writeOpEventHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

func readLastEventID(r *http.Request) string {
	lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if lastEventID == "" {
		lastEventID = strings.TrimSpace(r.URL.Query().Get("last_event_id"))
	}
	return lastEventID
}

func writeInitialOpEvents(
	w http.ResponseWriter,
	flusher http.Flusher,
	needsBootstrap bool,
	replay []opEventRecord,
	lastPayload *opEventPayload,
) bool {
	if needsBootstrap {
		bootstrap := *lastPayload
		bootstrap.EventID = "bootstrap"
		bootstrap.Message = "operation snapshot"
		writeErr := writeSSEEvent(w, flusher, opEventBootstrap, bootstrap, false)
		if writeErr != nil {
			return false
		}
	}

	for _, record := range replay {
		*lastPayload = record.Payload
		writeErr := writeSSEEvent(w, flusher, record.Name, record.Payload, true)
		if writeErr != nil {
			return false
		}
	}
	return true
}

func (a *API) streamLiveOpEvents(
	r *http.Request,
	w http.ResponseWriter,
	flusher http.Flusher,
	live <-chan opEventRecord,
	lastPayload opEventPayload,
) {
	heartbeatSeq := lastPayload.Sequence
	ticker := time.NewTicker(a.effectiveOpHeartbeatInterval())
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case record, streamOpen := <-live:
			if !streamOpen {
				return
			}
			lastPayload = record.Payload
			writeErr := writeSSEEvent(w, flusher, record.Name, record.Payload, true)
			if writeErr != nil {
				return
			}
		case <-ticker.C:
			heartbeatSeq++
			heartbeat := newOpHeartbeatPayload(lastPayload, heartbeatSeq)
			writeErr := writeSSEEvent(w, flusher, opEventHeartbeat, heartbeat, false)
			if writeErr != nil {
				return
			}
		}
	}
}

func (a *API) effectiveOpHeartbeatInterval() time.Duration {
	if a != nil && a.opHeartbeatInterval > 0 {
		return a.opHeartbeatInterval
	}
	return opEventsHeartbeatInterval
}

func writeSSEEvent(
	w http.ResponseWriter,
	flusher http.Flusher,
	eventName string,
	payload opEventPayload,
	includeProtocolID bool,
) error {
	payload.At = payload.At.UTC()
	if payload.At.IsZero() {
		payload.At = time.Now().UTC()
	}
	if payload.EventID == "" {
		payload.EventID = strconv.FormatInt(payload.Sequence, 10)
	}

	body, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return marshalErr
	}

	if includeProtocolID {
		// #nosec G705 -- SSE id field intentionally carries sanitized event identifiers.
		if _, err := w.Write([]byte("id: " + sanitizeSSEField(payload.EventID) + "\n")); err != nil {
			return err
		}
	}
	// #nosec G705 -- SSE event field intentionally carries sanitized event names.
	if _, err := w.Write([]byte("event: " + sanitizeSSEField(eventName) + "\n")); err != nil {
		return err
	}
	if _, err := w.Write([]byte("data: ")); err != nil {
		return err
	}
	// #nosec G705 -- SSE data payload intentionally streams JSON-encoded operation updates.
	if _, err := w.Write(body); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\n\n")); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func sanitizeSSEField(raw string) string {
	replacer := strings.NewReplacer("\n", " ", "\r", " ")
	return replacer.Replace(strings.TrimSpace(raw))
}
