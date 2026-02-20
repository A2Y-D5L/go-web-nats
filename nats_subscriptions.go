package platform

import (
	"encoding/json"

	"github.com/nats-io/nats.go"
)

////////////////////////////////////////////////////////////////////////////////
// NATS subscription for final deploy worker completion to wake API waiters
////////////////////////////////////////////////////////////////////////////////

func subscribeFinalResults(nc *nats.Conn, waiters *waiterHub) (*nats.Subscription, error) {
	sub, err := nc.Subscribe(subjectDeployDone, func(m *nats.Msg) {
		var msg WorkerResultMsg
		if err := json.Unmarshal(m.Data, &msg); err != nil {
			return
		}
		waiters.deliver(msg.OpID, msg)
	})
	if err != nil {
		return nil, err
	}
	return sub, nil
}
