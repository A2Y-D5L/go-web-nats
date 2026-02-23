package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	finalResultConsumerPrefix = "api_final_result_"
	finalResultUnknownText    = "unknown"
)

////////////////////////////////////////////////////////////////////////////////
// Durable JetStream consumers for final worker completions to wake API waiters.
////////////////////////////////////////////////////////////////////////////////

func subscribeFinalResults(
	ctx context.Context,
	js jetstream.JetStream,
	waiters *waiterHub,
	log sourceLogger,
) (func(), error) {
	subjects := finalResultSubjects()
	consumers := make([]jetstream.Consumer, 0, len(subjects))
	for _, subject := range subjects {
		err := ensureFinalResultConsumer(ctx, js, subject)
		if err != nil {
			return nil, err
		}
		consumer, err := js.Consumer(ctx, streamWorkerPipeline, finalResultConsumerName(subject))
		if err != nil {
			return nil, err
		}
		consumers = append(consumers, consumer)
	}

	consumeCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for i, consumer := range consumers {
		subject := subjects[i]
		wg.Add(1)
		go func(finalSubject string, finalConsumer jetstream.Consumer) {
			defer wg.Done()
			consumeFinalResults(consumeCtx, finalConsumer, waiters, finalSubject, log)
		}(subject, consumer)
	}

	stop := func() {
		cancel()
		wg.Wait()
	}
	return stop, nil
}

func finalResultSubjects() []string {
	return []string{
		subjectDeployDone,
		subjectDeploymentDone,
		subjectPromotionDone,
	}
}

func ensureFinalResultConsumer(
	ctx context.Context,
	js jetstream.JetStream,
	subject string,
) error {
	consumerName := finalResultConsumerName(subject)
	_, err := js.Consumer(ctx, streamWorkerPipeline, consumerName)
	if err == nil {
		return nil
	}
	if !errors.Is(err, jetstream.ErrConsumerNotFound) {
		return err
	}

	replayStart := time.Now().UTC().Add(-finalResultConsumerReplayWindow)

	var cfg jetstream.ConsumerConfig
	cfg.Name = consumerName
	cfg.Durable = consumerName
	cfg.Description = "api final-result waiter consumer for " + subject
	cfg.DeliverPolicy = jetstream.DeliverByStartTimePolicy
	cfg.OptStartTime = &replayStart
	cfg.AckPolicy = jetstream.AckExplicitPolicy
	cfg.AckWait = finalResultConsumerAckWait
	cfg.MaxDeliver = finalResultConsumerMaxDeliver
	cfg.BackOff = finalResultConsumerRetryBackoff()
	cfg.FilterSubject = subject
	cfg.ReplayPolicy = jetstream.ReplayInstantPolicy
	cfg.MaxAckPending = 1
	_, err = js.CreateConsumer(ctx, streamWorkerPipeline, cfg)
	return err
}

func finalResultConsumerName(subject string) string {
	sanitized := strings.TrimSpace(subject)
	sanitized = strings.ReplaceAll(sanitized, ".", "_")
	sanitized = strings.ReplaceAll(sanitized, " ", "_")
	if sanitized == "" {
		sanitized = finalResultUnknownText
	}
	return finalResultConsumerPrefix + sanitized
}

func consumeFinalResults(
	ctx context.Context,
	consumer jetstream.Consumer,
	waiters *waiterHub,
	subject string,
	log sourceLogger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, nextErr := consumer.Next(jetstream.FetchMaxWait(finalResultConsumerFetchWait))
		if nextErr != nil {
			if errors.Is(nextErr, nats.ErrTimeout) ||
				errors.Is(nextErr, context.DeadlineExceeded) ||
				errors.Is(nextErr, jetstream.ErrMsgIteratorClosed) {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			log.Warnf("final waiter consumer next error subject=%s: %v", subject, nextErr)
			continue
		}

		opID, outcome := deliverFinalResultMessage(waiters, msg.Data(), log)
		ackErr := msg.Ack()
		if ackErr != nil {
			log.Warnf(
				"final waiter consumer ack error subject=%s op=%s outcome=%s: %v",
				subject,
				opID,
				waiterDeliveryOutcomeText(outcome),
				ackErr,
			)
		}
	}
}

func deliverFinalResultMessage(
	waiters *waiterHub,
	payload []byte,
	log sourceLogger,
) (string, waiterDeliveryOutcome) {
	var msg WorkerResultMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Warnf("final waiter decode error: %v", err)
		return "", waiterDeliveryNoWaiter
	}
	opID := strings.TrimSpace(msg.OpID)
	if waiters == nil {
		return opID, waiterDeliveryNoWaiter
	}
	return opID, waiters.deliver(opID, msg)
}

func waiterDeliveryOutcomeText(outcome waiterDeliveryOutcome) string {
	switch outcome {
	case waiterDeliveryNoWaiter:
		return "no_waiter"
	case waiterDeliveryDelivered:
		return "delivered"
	case waiterDeliveryDuplicate:
		return "duplicate"
	default:
		return finalResultUnknownText
	}
}
