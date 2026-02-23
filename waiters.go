package platform

import (
	"strings"
	"sync"
	"time"
)

////////////////////////////////////////////////////////////////////////////////
// Wait hub for API: wait for final worker result by op_id
////////////////////////////////////////////////////////////////////////////////

type waiterDeliveryOutcome int

const (
	waiterDeliveryNoWaiter waiterDeliveryOutcome = iota
	waiterDeliveryDelivered
	waiterDeliveryDuplicate
)

type waiterHub struct {
	mu             sync.Mutex
	waiters        map[string]chan WorkerResultMsg
	delivered      map[string]time.Time
	deliveredOrder []string
	deliveredTTL   time.Duration
	deliveredCap   int
}

func newWaiterHub() *waiterHub {
	return newWaiterHubWithConfig(
		finalResultWaiterDeliveryTTL,
		finalResultWaiterDeliveryCacheMax,
	)
}

func newWaiterHubWithConfig(deliveredTTL time.Duration, deliveredCap int) *waiterHub {
	if deliveredTTL <= 0 {
		deliveredTTL = finalResultWaiterDeliveryTTL
	}
	if deliveredCap <= 0 {
		deliveredCap = finalResultWaiterDeliveryCacheMax
	}
	return &waiterHub{
		mu:             sync.Mutex{},
		waiters:        map[string]chan WorkerResultMsg{},
		delivered:      map[string]time.Time{},
		deliveredOrder: []string{},
		deliveredTTL:   deliveredTTL,
		deliveredCap:   deliveredCap,
	}
}

func (h *waiterHub) register(opID string) <-chan WorkerResultMsg {
	opID = strings.TrimSpace(opID)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanupDeliveredLocked(time.Now().UTC())
	ch := make(chan WorkerResultMsg, 1)
	h.waiters[opID] = ch
	return ch
}

func (h *waiterHub) unregister(opID string) {
	opID = strings.TrimSpace(opID)
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.waiters, opID)
}

func (h *waiterHub) deliver(opID string, msg WorkerResultMsg) waiterDeliveryOutcome {
	opID = strings.TrimSpace(opID)
	if opID == "" {
		return waiterDeliveryNoWaiter
	}
	now := time.Now().UTC()

	h.mu.Lock()
	h.cleanupDeliveredLocked(now)
	if _, seen := h.delivered[opID]; seen {
		h.mu.Unlock()
		return waiterDeliveryDuplicate
	}
	ch, ok := h.waiters[opID]
	if ok {
		delete(h.waiters, opID)
	}
	h.markDeliveredLocked(opID, now)
	h.mu.Unlock()
	if !ok {
		return waiterDeliveryNoWaiter
	}
	select {
	case ch <- msg:
	default:
	}
	return waiterDeliveryDelivered
}

func (h *waiterHub) markDeliveredLocked(opID string, at time.Time) {
	if _, exists := h.delivered[opID]; exists {
		return
	}
	h.delivered[opID] = at
	h.deliveredOrder = append(h.deliveredOrder, opID)
	h.cleanupDeliveredLocked(at)
}

func (h *waiterHub) cleanupDeliveredLocked(now time.Time) {
	if len(h.delivered) == 0 {
		h.deliveredOrder = h.deliveredOrder[:0]
		return
	}
	if h.deliveredTTL > 0 {
		cutoff := now.Add(-h.deliveredTTL)
		for len(h.deliveredOrder) > 0 {
			oldest := h.deliveredOrder[0]
			deliveredAt, ok := h.delivered[oldest]
			if !ok {
				h.deliveredOrder = h.deliveredOrder[1:]
				continue
			}
			if deliveredAt.After(cutoff) {
				break
			}
			delete(h.delivered, oldest)
			h.deliveredOrder = h.deliveredOrder[1:]
		}
	}
	for len(h.deliveredOrder) > 0 && len(h.delivered) > h.deliveredCap {
		oldest := h.deliveredOrder[0]
		h.deliveredOrder = h.deliveredOrder[1:]
		delete(h.delivered, oldest)
	}
}
