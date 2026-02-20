//nolint:exhaustruct // Test fixtures intentionally use partial structs for readability.
package platform_test

import (
	"sync"
	"testing"
	"time"

	platform "github.com/a2y-d5l/go-web-nats"
)

func TestWorkers_WaiterHubRegisterDeliver(t *testing.T) {
	h := platform.NewWaiterHubForTest()
	ch := h.Register("op-1")

	h.Deliver("op-1", platform.WorkerResultMsg{OpID: "op-1", Message: "done"})

	select {
	case got := <-ch:
		if got.OpID != "op-1" {
			t.Fatalf("unexpected op id: got %q", got.OpID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for waiter delivery")
	}
}

func TestWorkers_WaiterHubUnregisterAndDeliverNoPanic(_ *testing.T) {
	h := platform.NewWaiterHubForTest()

	for range 100 {
		opID := "op-race"
		h.Register(opID)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 200 {
				h.Deliver(opID, platform.WorkerResultMsg{OpID: opID})
			}
		}()
		go func() {
			defer wg.Done()
			h.Unregister(opID)
		}()
		wg.Wait()
	}
}
