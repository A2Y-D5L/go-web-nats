package main

import (
	"context"
	"encoding/json"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func startWorker(
	ctx context.Context,
	workerName, natsURL, inSubj, outSubj string,
	artifacts ArtifactStore,
	fn workerFn,
) error {
	workerLog := appLoggerForProcess().Source(workerName)
	go runWorkerLoop(ctx, workerName, natsURL, inSubj, outSubj, artifacts, fn, workerLog)

	return nil
}

func runWorkerLoop(
	ctx context.Context,
	workerName, natsURL, inSubj, outSubj string,
	artifacts ArtifactStore,
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
	workerLog.Infof("ready: subscribe=%s publish=%s", inSubj, outSubj)

	sub, err := nc.Subscribe(inSubj, func(m *nats.Msg) {
		handleWorkerMessage(
			ctx,
			store,
			artifacts,
			workerName,
			inSubj,
			outSubj,
			fn,
			nc,
			m,
			workerLog,
		)
	})
	if err != nil {
		workerLog.Errorf("subscribe error: %v", err)
		return
	}
	defer func() {
		if unSubErr := sub.Unsubscribe(); unSubErr != nil {
			workerLog.Warnf("unsubscribe error: %v", unSubErr)
		}
	}()

	_ = nc.Flush()
	<-ctx.Done()
}

func handleWorkerMessage(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	workerName, inSubj, outSubj string,
	fn workerFn,
	nc *nats.Conn,
	m *nats.Msg,
	workerLog sourceLogger,
) {
	var opMsg ProjectOpMsg
	unmarshalErr := json.Unmarshal(m.Data, &opMsg)
	if unmarshalErr != nil {
		workerLog.Warnf("discarding invalid message on %s: %v", inSubj, unmarshalErr)
		return
	}
	if opMsg.Err != "" {
		workerLog.Warnf("skip op=%s due to upstream error: %s", opMsg.OpID, opMsg.Err)
		publishErr := publishWorkerResult(nc, outSubj, skipWorkerResult(opMsg, workerName))
		if publishErr != nil {
			workerLog.Errorf(
				"publish result failed op=%s subject=%s: %v",
				opMsg.OpID,
				outSubj,
				publishErr,
			)
		}
		return
	}

	workerLog.Infof("start op=%s kind=%s project=%s", opMsg.OpID, opMsg.Kind, opMsg.ProjectID)
	res, workerErr := fn(ctx, store, artifacts, opMsg)
	if workerErr != nil {
		res.Err = workerErr.Error()
		workerLog.Errorf("op=%s failed: %v", opMsg.OpID, workerErr)
	} else {
		workerLog.Infof("done op=%s message=%q artifacts=%d", opMsg.OpID, res.Message, len(res.Artifacts))
	}
	publishErr := publishWorkerResult(nc, outSubj, finalizeWorkerResult(opMsg, workerName, res))
	if publishErr != nil {
		workerLog.Errorf(
			"publish result failed op=%s subject=%s: %v",
			opMsg.OpID,
			outSubj,
			publishErr,
		)
	}
}
