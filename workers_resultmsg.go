package platform

import (
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
)

func skipWorkerResult(opMsg ProjectOpMsg, workerName string) WorkerResultMsg {
	res := newWorkerResultMsg("skipped due to upstream error")
	res.OpID = opMsg.OpID
	res.Kind = opMsg.Kind
	res.ProjectID = opMsg.ProjectID
	res.Spec = opMsg.Spec
	res.Worker = workerName
	res.Err = opMsg.Err
	res.At = time.Now().UTC()
	return res
}

func finalizeWorkerResult(
	opMsg ProjectOpMsg,
	workerName string,
	res WorkerResultMsg,
) WorkerResultMsg {
	res.Worker = workerName
	res.OpID = opMsg.OpID
	res.Kind = opMsg.Kind
	res.ProjectID = opMsg.ProjectID
	res.Spec = opMsg.Spec
	if res.Err == "" {
		res.Err = opMsg.Err
	}
	res.At = time.Now().UTC()
	return res
}

func publishWorkerResult(nc *nats.Conn, subject string, res WorkerResultMsg) error {
	body, _ := json.Marshal(res)
	return nc.Publish(subject, body)
}
