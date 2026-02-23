package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func skipWorkerResult(opMsg ProjectOpMsg, workerName string) WorkerResultMsg {
	res := newWorkerResultMsg("skipped due to upstream error")
	res.OpID = opMsg.OpID
	res.Kind = opMsg.Kind
	res.ProjectID = opMsg.ProjectID
	res.Spec = opMsg.Spec
	res.DeployEnv = opMsg.DeployEnv
	res.FromEnv = opMsg.FromEnv
	res.ToEnv = opMsg.ToEnv
	res.RollbackReleaseID = opMsg.RollbackReleaseID
	res.RollbackEnv = opMsg.RollbackEnv
	res.RollbackScope = opMsg.RollbackScope
	res.RollbackOverride = opMsg.RollbackOverride
	res.Delivery = opMsg.Delivery
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
	res.DeployEnv = opMsg.DeployEnv
	res.FromEnv = opMsg.FromEnv
	res.ToEnv = opMsg.ToEnv
	res.RollbackReleaseID = opMsg.RollbackReleaseID
	res.RollbackEnv = opMsg.RollbackEnv
	res.RollbackScope = opMsg.RollbackScope
	res.RollbackOverride = opMsg.RollbackOverride
	res.Delivery = opMsg.Delivery
	if res.Err == "" {
		res.Err = opMsg.Err
	}
	res.At = time.Now().UTC()
	return res
}

func publishWorkerResult(
	ctx context.Context,
	js jetstream.JetStream,
	subject string,
	res WorkerResultMsg,
) error {
	body, err := json.Marshal(res)
	if err != nil {
		return err
	}
	_, err = js.Publish(ctx, subject, body, jetstream.WithMsgID(workerResultMessageID(subject, res)))
	return err
}

func publishWorkerPoison(
	ctx context.Context,
	js jetstream.JetStream,
	msg WorkerPoisonMsg,
) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = js.Publish(
		ctx,
		subjectWorkerPoison,
		body,
		jetstream.WithMsgID(workerPoisonMessageID(msg)),
	)
	return err
}

func workerResultMessageID(subject string, res WorkerResultMsg) string {
	return fmt.Sprintf(
		"worker-result:%s:%s:%s",
		sanitizeMessageIDComponent(subject),
		sanitizeMessageIDComponent(res.OpID),
		sanitizeMessageIDComponent(res.Worker),
	)
}

func workerPoisonMessageID(msg WorkerPoisonMsg) string {
	return fmt.Sprintf(
		"worker-poison:%s:%s:%s:%d",
		sanitizeMessageIDComponent(msg.Worker),
		sanitizeMessageIDComponent(msg.SubjectIn),
		sanitizeMessageIDComponent(msg.OpID),
		msg.Attempt,
	)
}

func sanitizeMessageIDComponent(in string) string {
	in = strings.TrimSpace(in)
	in = strings.ReplaceAll(in, ":", "_")
	in = strings.ReplaceAll(in, " ", "_")
	if in == "" {
		return "none"
	}
	return in
}
