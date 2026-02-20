package platform

import (
	"encoding/json"

	"github.com/nats-io/nats.go"
)

////////////////////////////////////////////////////////////////////////////////
// NATS subscriptions for final worker completions to wake API waiters
////////////////////////////////////////////////////////////////////////////////

func subscribeFinalResults(nc *nats.Conn, waiters *waiterHub) ([]*nats.Subscription, error) {
	subjects := []string{
		subjectDeployDone,
		subjectDeploymentDone,
		subjectPromotionDone,
	}
	subs := make([]*nats.Subscription, 0, len(subjects))
	for _, subject := range subjects {
		sub, err := nc.Subscribe(subject, func(m *nats.Msg) {
			var msg WorkerResultMsg
			if err := json.Unmarshal(m.Data, &msg); err != nil {
				return
			}
			waiters.deliver(msg.OpID, msg)
		})
		if err != nil {
			for _, existing := range subs {
				_ = existing.Unsubscribe()
			}
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, nil
}
