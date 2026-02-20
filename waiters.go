package main

import "sync"

////////////////////////////////////////////////////////////////////////////////
// Wait hub for API: wait for final worker result by op_id
////////////////////////////////////////////////////////////////////////////////

type waiterHub struct {
	mu      sync.Mutex
	waiters map[string]chan WorkerResultMsg
}

func newWaiterHub() *waiterHub {
	return &waiterHub{
		mu:      sync.Mutex{},
		waiters: map[string]chan WorkerResultMsg{},
	}
}

func (h *waiterHub) register(opID string) <-chan WorkerResultMsg {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan WorkerResultMsg, 1)
	h.waiters[opID] = ch
	return ch
}

func (h *waiterHub) unregister(opID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.waiters, opID)
}

func (h *waiterHub) deliver(opID string, msg WorkerResultMsg) {
	h.mu.Lock()
	ch, ok := h.waiters[opID]
	h.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}
