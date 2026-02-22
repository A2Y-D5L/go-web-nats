package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const workerPoisonPayloadLimit = 2048

type workerDeliveryAction int

const (
	workerDeliveryAck workerDeliveryAction = iota
	workerDeliveryRetry
	workerDeliveryTerminate
)

type workerDeliveryDecision struct {
	action     workerDeliveryAction
	retryDelay time.Duration
}

type workerResultPublishFn func(
	ctx context.Context,
	js jetstream.JetStream,
	subject string,
	res WorkerResultMsg,
) error

type workerPoisonPublishFn func(
	ctx context.Context,
	js jetstream.JetStream,
	msg WorkerPoisonMsg,
) error

func startWorker(
	ctx context.Context,
	workerName, natsURL, inSubj, outSubj string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
	fn workerFn,
) error {
	workerLog := appLoggerForProcess().Source(workerName)
	go runWorkerLoop(
		ctx,
		workerName,
		natsURL,
		inSubj,
		outSubj,
		artifacts,
		opEvents,
		fn,
		workerLog,
	)

	return nil
}

func runWorkerLoop(
	ctx context.Context,
	workerName, natsURL, inSubj, outSubj string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
	fn workerFn,
	workerLog sourceLogger,
) {
	nc, err := nats.Connect(natsURL, nats.Name(workerName))
	if err != nil {
		workerLog.Errorf("connect error: %v", err)
		return
	}
	defer func() {
		if drainErr := nc.Drain(); drainErr != nil {
			workerLog.Warnf("drain error: %v", drainErr)
		}
	}()

	js, err := jetstream.New(nc)
	if err != nil {
		workerLog.Errorf("jetstream error: %v", err)
		return
	}
	store, err := newStore(ctx, js)
	if err != nil {
		workerLog.Errorf("store error: %v", err)
		return
	}
	store.setOpEvents(opEvents)

	streamErr := ensureWorkerDeliveryStream(ctx, js)
	if streamErr != nil {
		workerLog.Errorf("worker stream setup error: %v", streamErr)
		return
	}

	consumerName := workerConsumerName(workerName)
	var consumerCfg jetstream.ConsumerConfig
	consumerCfg.Name = consumerName
	consumerCfg.Durable = consumerName
	consumerCfg.Description = fmt.Sprintf("worker %s consumer for %s", workerName, inSubj)
	consumerCfg.DeliverPolicy = jetstream.DeliverAllPolicy
	consumerCfg.AckPolicy = jetstream.AckExplicitPolicy
	consumerCfg.AckWait = workerDeliveryAckWait
	consumerCfg.MaxDeliver = workerDeliveryMaxDeliver
	consumerCfg.BackOff = workerDeliveryRetryBackoff()
	consumerCfg.FilterSubject = inSubj
	consumerCfg.ReplayPolicy = jetstream.ReplayInstantPolicy
	consumerCfg.MaxAckPending = 1

	consumer, err := js.CreateOrUpdateConsumer(ctx, streamWorkerPipeline, consumerCfg)
	if err != nil {
		workerLog.Errorf("consumer setup error: %v", err)
		return
	}

	workerLog.Infof("ready: subscribe=%s publish=%s", inSubj, outSubj)
	consumeWorkerMessages(
		ctx,
		store,
		consumer,
		artifacts,
		workerName,
		inSubj,
		outSubj,
		fn,
		js,
		workerLog,
	)
}

func consumeWorkerMessages(
	ctx context.Context,
	store *Store,
	consumer jetstream.Consumer,
	artifacts ArtifactStore,
	workerName, inSubj, outSubj string,
	fn workerFn,
	js jetstream.JetStream,
	workerLog sourceLogger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, nextErr := consumer.Next(jetstream.FetchMaxWait(workerDeliveryFetchWait))
		if nextErr != nil {
			if errors.Is(nextErr, nats.ErrTimeout) ||
				errors.Is(nextErr, context.DeadlineExceeded) ||
				errors.Is(nextErr, jetstream.ErrMsgIteratorClosed) {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			workerLog.Warnf("consumer next error on %s: %v", inSubj, nextErr)
			continue
		}

		attempt := workerDeliveryAttempt(msg)
		decision := handleWorkerDelivery(
			ctx,
			store,
			artifacts,
			workerName,
			inSubj,
			outSubj,
			fn,
			js,
			msg.Data(),
			attempt,
			workerLog,
			publishWorkerResult,
			publishWorkerPoison,
		)
		applyWorkerDeliveryDecision(msg, decision, workerLog)
	}
}

func workerConsumerName(workerName string) string {
	sanitized := strings.TrimSpace(workerName)
	if sanitized == "" {
		sanitized = "worker"
	}
	return "worker_" + strings.ReplaceAll(sanitized, "-", "_")
}

func workerDeliveryAttempt(msg jetstream.Msg) uint64 {
	meta, err := msg.Metadata()
	if err != nil || meta == nil || meta.NumDelivered == 0 {
		return 1
	}
	return meta.NumDelivered
}

func handleWorkerDelivery(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	workerName, inSubj, outSubj string,
	fn workerFn,
	js jetstream.JetStream,
	rawPayload []byte,
	attempt uint64,
	workerLog sourceLogger,
	resultPublisher workerResultPublishFn,
	poisonPublisher workerPoisonPublishFn,
) workerDeliveryDecision {
	opMsg, decodeErr := decodeWorkerOpMsg(rawPayload)
	if decodeErr != nil {
		reason := fmt.Sprintf("invalid worker message payload: %v", decodeErr)
		workerLog.Warnf("poison message on %s: %s", inSubj, reason)
		storeWorkerPoison(
			ctx,
			js,
			workerName,
			inSubj,
			outSubj,
			nil,
			attempt,
			reason,
			rawPayload,
			workerLog,
			poisonPublisher,
		)
		return workerTerminateDecision()
	}

	preDecision, handled := handleWorkerPreExecution(
		ctx,
		store,
		artifacts,
		workerName,
		inSubj,
		outSubj,
		opMsg,
		js,
		attempt,
		rawPayload,
		workerLog,
		resultPublisher,
		poisonPublisher,
	)
	if handled {
		return preDecision
	}

	return executeWorkerAndPublish(
		ctx,
		store,
		artifacts,
		workerName,
		inSubj,
		outSubj,
		opMsg,
		fn,
		js,
		attempt,
		rawPayload,
		workerLog,
		resultPublisher,
		poisonPublisher,
	)
}

func decodeWorkerOpMsg(rawPayload []byte) (ProjectOpMsg, error) {
	var opMsg ProjectOpMsg
	if err := json.Unmarshal(rawPayload, &opMsg); err != nil {
		return ProjectOpMsg{}, err
	}
	if strings.TrimSpace(opMsg.OpID) == "" {
		return ProjectOpMsg{}, errors.New("op_id is required")
	}
	return opMsg, nil
}

func handleWorkerPreExecution(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	workerName, inSubj, outSubj string,
	opMsg ProjectOpMsg,
	js jetstream.JetStream,
	attempt uint64,
	rawPayload []byte,
	workerLog sourceLogger,
	resultPublisher workerResultPublishFn,
	poisonPublisher workerPoisonPublishFn,
) (workerDeliveryDecision, bool) {
	completedRes, alreadyProcessed, lookupErr := completedWorkerResultForDelivery(ctx, store, opMsg, workerName)
	if lookupErr != nil {
		return workerRetryOrPoison(
			ctx,
			store,
			artifacts,
			js,
			workerName,
			inSubj,
			outSubj,
			&opMsg,
			attempt,
			rawPayload,
			fmt.Sprintf("lookup completed step: %v", lookupErr),
			workerLog,
			resultPublisher,
			poisonPublisher,
		), true
	}
	if alreadyProcessed {
		publishErr := resultPublisher(ctx, js, outSubj, completedRes)
		if publishErr != nil {
			return workerRetryOrPoison(
				ctx,
				store,
				artifacts,
				js,
				workerName,
				inSubj,
				outSubj,
				&opMsg,
				attempt,
				rawPayload,
				fmt.Sprintf("publish duplicate-completion result: %v", publishErr),
				workerLog,
				resultPublisher,
				poisonPublisher,
			), true
		}
		workerLog.Infof(
			"duplicate delivery op=%s worker=%s attempt=%d reused persisted step result",
			opMsg.OpID,
			workerName,
			attempt,
		)
		return workerAckDecision(), true
	}
	if opMsg.Err != "" {
		workerLog.Warnf("skip op=%s due to upstream error: %s", opMsg.OpID, opMsg.Err)
		publishErr := resultPublisher(ctx, js, outSubj, skipWorkerResult(opMsg, workerName))
		if publishErr != nil {
			return workerRetryOrPoison(
				ctx,
				store,
				artifacts,
				js,
				workerName,
				inSubj,
				outSubj,
				&opMsg,
				attempt,
				rawPayload,
				fmt.Sprintf("publish upstream-skip result: %v", publishErr),
				workerLog,
				resultPublisher,
				poisonPublisher,
			), true
		}
		return workerAckDecision(), true
	}
	return workerDeliveryDecision{}, false
}

func executeWorkerAndPublish(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	workerName, inSubj, outSubj string,
	opMsg ProjectOpMsg,
	fn workerFn,
	js jetstream.JetStream,
	attempt uint64,
	rawPayload []byte,
	workerLog sourceLogger,
	resultPublisher workerResultPublishFn,
	poisonPublisher workerPoisonPublishFn,
) workerDeliveryDecision {
	workerLog.Infof("start op=%s kind=%s project=%s", opMsg.OpID, opMsg.Kind, opMsg.ProjectID)
	res, workerErr := fn(ctx, store, artifacts, opMsg)
	if workerErr != nil {
		res.Err = workerErr.Error()
		workerLog.Errorf("op=%s failed: %v", opMsg.OpID, workerErr)
	} else {
		workerLog.Infof("done op=%s message=%q artifacts=%d", opMsg.OpID, res.Message, len(res.Artifacts))
	}
	publishErr := resultPublisher(ctx, js, outSubj, finalizeWorkerResult(opMsg, workerName, res))
	if publishErr != nil {
		return workerRetryOrPoison(
			ctx,
			store,
			artifacts,
			js,
			workerName,
			inSubj,
			outSubj,
			&opMsg,
			attempt,
			rawPayload,
			fmt.Sprintf("publish worker result: %v", publishErr),
			workerLog,
			resultPublisher,
			poisonPublisher,
		)
	}
	return workerAckDecision()
}

func completedWorkerResultForDelivery(
	ctx context.Context,
	store *Store,
	opMsg ProjectOpMsg,
	workerName string,
) (WorkerResultMsg, bool, error) {
	op, err := store.GetOp(ctx, opMsg.OpID)
	if err != nil {
		return WorkerResultMsg{}, false, err
	}
	for i := len(op.Steps) - 1; i >= 0; i-- {
		step := op.Steps[i]
		if step.Worker != workerName || step.EndedAt.IsZero() {
			continue
		}
		message := strings.TrimSpace(step.Message)
		if message == "" {
			if strings.TrimSpace(step.Error) != "" {
				message = "worker step already failed on prior delivery"
			} else {
				message = "worker step already completed on prior delivery"
			}
		}
		res := newWorkerResultMsg(message)
		res.Err = strings.TrimSpace(step.Error)
		res.Artifacts = append([]string(nil), step.Artifacts...)
		return finalizeWorkerResult(opMsg, workerName, res), true, nil
	}
	return WorkerResultMsg{}, false, nil
}

func workerRetryOrPoison(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	js jetstream.JetStream,
	workerName, inSubj, outSubj string,
	opMsg *ProjectOpMsg,
	attempt uint64,
	rawPayload []byte,
	reason string,
	workerLog sourceLogger,
	resultPublisher workerResultPublishFn,
	poisonPublisher workerPoisonPublishFn,
) workerDeliveryDecision {
	if attempt < uint64(workerDeliveryMaxDeliver) {
		delay := workerRetryDelay(attempt)
		workerLog.Warnf(
			"delivery retry op=%s worker=%s attempt=%d/%d delay=%s reason=%s",
			opIDFromMessage(opMsg),
			workerName,
			attempt,
			workerDeliveryMaxDeliver,
			delay,
			reason,
		)
		return workerRetryDecision(delay)
	}

	finalReason := fmt.Sprintf(
		"worker delivery exhausted retries worker=%s op=%s attempts=%d reason=%s",
		workerName,
		opIDFromMessage(opMsg),
		attempt,
		reason,
	)
	workerLog.Errorf("%s", finalReason)
	storeWorkerPoison(
		ctx,
		js,
		workerName,
		inSubj,
		outSubj,
		opMsg,
		attempt,
		finalReason,
		rawPayload,
		workerLog,
		poisonPublisher,
	)
	if opMsg != nil {
		markWorkerDeliveryFailure(ctx, store, artifacts, *opMsg, finalReason, workerLog)
		poisonResult := poisonWorkerResult(*opMsg, workerName, finalReason)
		if err := resultPublisher(ctx, js, outSubj, poisonResult); err != nil {
			workerLog.Errorf(
				"publish poison result failed op=%s subject=%s: %v",
				opMsg.OpID,
				outSubj,
				err,
			)
		}
	}
	return workerTerminateDecision()
}

func storeWorkerPoison(
	ctx context.Context,
	js jetstream.JetStream,
	workerName, inSubj, outSubj string,
	opMsg *ProjectOpMsg,
	attempt uint64,
	reason string,
	rawPayload []byte,
	workerLog sourceLogger,
	poisonPublisher workerPoisonPublishFn,
) {
	msg := WorkerPoisonMsg{
		Worker:     workerName,
		SubjectIn:  inSubj,
		SubjectOut: outSubj,
		OpID:       "",
		Kind:       "",
		ProjectID:  "",
		Delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Attempt:    attempt,
		MaxDeliver: workerDeliveryMaxDeliver,
		Reason:     reason,
		RawPayload: truncatedPayload(rawPayload, workerPoisonPayloadLimit),
		StoredAt:   time.Now().UTC(),
	}
	if opMsg != nil {
		msg.OpID = opMsg.OpID
		msg.Kind = opMsg.Kind
		msg.ProjectID = opMsg.ProjectID
		msg.Delivery = opMsg.Delivery
	}
	if err := poisonPublisher(ctx, js, msg); err != nil {
		workerLog.Errorf("publish poison marker failed worker=%s op=%s: %v", workerName, msg.OpID, err)
	}
}

func poisonWorkerResult(opMsg ProjectOpMsg, workerName, reason string) WorkerResultMsg {
	res := newWorkerResultMsg("worker delivery retries exhausted")
	res.Err = reason
	return finalizeWorkerResult(opMsg, workerName, res)
}

func markWorkerDeliveryFailure(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	opMsg ProjectOpMsg,
	reason string,
	workerLog sourceLogger,
) {
	op, err := store.GetOp(ctx, opMsg.OpID)
	if err != nil {
		workerLog.Warnf("read op for poison finalize op=%s failed: %v", opMsg.OpID, err)
		return
	}
	if op.Status == opStatusDone || op.Status == opStatusError {
		return
	}
	finalizeErr := finalizeOp(
		context.WithoutCancel(ctx),
		store,
		op.ID,
		op.ProjectID,
		op.Kind,
		opStatusError,
		reason,
	)
	if finalizeErr != nil {
		workerLog.Warnf("finalize op on poison failure op=%s failed: %v", opMsg.OpID, finalizeErr)
	}
	if op.Kind == OpCI {
		stateErr := finalizeSourceCommitPendingOp(artifacts, op.ProjectID, op.ID, false)
		if stateErr != nil {
			workerLog.Warnf("finalize ci commit state on poison failure op=%s failed: %v", opMsg.OpID, stateErr)
		}
	}
}

func workerRetryDelay(attempt uint64) time.Duration {
	backoff := workerDeliveryRetryBackoff()
	if len(backoff) == 0 {
		return 0
	}

	idx := 0
	remaining := attempt
	for remaining > 1 && idx < len(backoff)-1 {
		idx++
		remaining--
	}
	return backoff[idx]
}

func truncatedPayload(raw []byte, limit int) string {
	if len(raw) == 0 {
		return ""
	}
	if limit <= 0 || len(raw) <= limit {
		return string(raw)
	}
	return string(raw[:limit]) + "...(truncated)"
}

func opIDFromMessage(opMsg *ProjectOpMsg) string {
	if opMsg == nil || strings.TrimSpace(opMsg.OpID) == "" {
		return "unknown"
	}
	return opMsg.OpID
}

func applyWorkerDeliveryDecision(
	msg jetstream.Msg,
	decision workerDeliveryDecision,
	workerLog sourceLogger,
) {
	switch decision.action {
	case workerDeliveryAck:
		if err := msg.Ack(); err != nil {
			workerLog.Warnf("worker message ack failed: %v", err)
		}
	case workerDeliveryRetry:
		var err error
		if decision.retryDelay > 0 {
			err = msg.NakWithDelay(decision.retryDelay)
		} else {
			err = msg.Nak()
		}
		if err != nil {
			workerLog.Warnf("worker message nack failed: %v", err)
		}
	case workerDeliveryTerminate:
		if err := msg.Term(); err != nil {
			workerLog.Warnf("worker message term failed: %v", err)
		}
	}
}

func workerAckDecision() workerDeliveryDecision {
	return workerDeliveryDecision{
		action:     workerDeliveryAck,
		retryDelay: 0,
	}
}

func workerRetryDecision(delay time.Duration) workerDeliveryDecision {
	return workerDeliveryDecision{
		action:     workerDeliveryRetry,
		retryDelay: delay,
	}
}

func workerTerminateDecision() workerDeliveryDecision {
	return workerDeliveryDecision{
		action:     workerDeliveryTerminate,
		retryDelay: 0,
	}
}
