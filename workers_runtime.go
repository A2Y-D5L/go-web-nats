package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

////////////////////////////////////////////////////////////////////////////////
// Workers (registration -> bootstrap -> build -> deploy)
////////////////////////////////////////////////////////////////////////////////

type Worker interface {
	Start(ctx context.Context) error
}

type WorkerBase struct {
	name       string
	natsURL    string
	subjectIn  string
	subjectOut string
	artifacts  ArtifactStore
}

func newWorkerBase(
	name, natsURL, subjectIn, subjectOut string,
	artifacts ArtifactStore,
) WorkerBase {
	return WorkerBase{
		name:       name,
		natsURL:    natsURL,
		subjectIn:  subjectIn,
		subjectOut: subjectOut,
		artifacts:  artifacts,
	}
}

type (
	RegistrationWorker     struct{ WorkerBase }
	RepoBootstrapWorker    struct{ WorkerBase }
	ImageBuilderWorker     struct{ WorkerBase }
	ManifestRendererWorker struct{ WorkerBase }
)

func NewRegistrationWorker(natsURL string, artifacts ArtifactStore) *RegistrationWorker {
	return &RegistrationWorker{
		WorkerBase: newWorkerBase(
			"registrar",
			natsURL,
			subjectProjectOpStart,
			subjectRegistrationDone,
			artifacts,
		),
	}
}

func NewRepoBootstrapWorker(natsURL string, artifacts ArtifactStore) *RepoBootstrapWorker {
	return &RepoBootstrapWorker{
		WorkerBase: newWorkerBase(
			"repoBootstrap",
			natsURL,
			subjectRegistrationDone,
			subjectBootstrapDone,
			artifacts,
		),
	}
}

func NewImageBuilderWorker(natsURL string, artifacts ArtifactStore) *ImageBuilderWorker {
	return &ImageBuilderWorker{
		WorkerBase: newWorkerBase(
			"imageBuilder",
			natsURL,
			subjectBootstrapDone,
			subjectBuildDone,
			artifacts,
		),
	}
}

func NewManifestRendererWorker(natsURL string, artifacts ArtifactStore) *ManifestRendererWorker {
	return &ManifestRendererWorker{
		WorkerBase: newWorkerBase(
			"manifestRenderer",
			natsURL,
			subjectBuildDone,
			subjectDeployDone,
			artifacts,
		),
	}
}

func (w *RegistrationWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		registrationWorkerAction,
	)
}

func (w *RepoBootstrapWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		repoBootstrapWorkerAction,
	)
}

func (w *ImageBuilderWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		imageBuilderWorkerAction,
	)
}

func (w *ManifestRendererWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		manifestRendererWorkerAction,
	)
}

type workerFn func(ctx context.Context, store *Store, artifacts ArtifactStore, msg ProjectOpMsg) (WorkerResultMsg, error)

// startWorker subscribes to one subject (unique per worker), does work, and publishes a result for the next worker.
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
