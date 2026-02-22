package platform

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Run starts the local platform runtime (embedded NATS, workers, and HTTP API).
func Run() {
	mainLog := appLoggerForProcess().Source("main")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ns, natsURL, jsDir, err := startEmbeddedNATS()
	if err != nil {
		mainLog.Fatalf("start embedded nats: %v", err)
	}
	defer func() {
		ns.Shutdown()
		ns.WaitForShutdown()
		_ = os.RemoveAll(jsDir)
	}()

	nc, err := nats.Connect(natsURL, nats.Name("api"))
	if err != nil {
		mainLog.Fatalf("connect nats: %v", err)
	}
	defer func() {
		if derr := nc.Drain(); derr != nil {
			mainLog.Warnf("nats drain error: %v", derr)
		}
	}()

	js, err := jetstream.New(nc)
	if err != nil {
		mainLog.Fatalf("jetstream: %v", err)
	}

	store, err := newStore(ctx, js)
	if err != nil {
		mainLog.Fatalf("store: %v", err)
	}
	opEvents := newOpEventHub(opEventsHistoryLimit, opEventsRetention)
	store.setOpEvents(opEvents)

	artifacts := NewFSArtifacts(defaultArtifactsRoot)
	mkdirErr := os.MkdirAll(defaultArtifactsRoot, dirModePrivateRead)
	if mkdirErr != nil {
		mainLog.Fatalf("mkdir artifacts root: %v", mkdirErr)
	}

	startErr := startPlatformWorkers(ctx, natsURL, artifacts, opEvents)
	if startErr != nil {
		mainLog.Fatalf("start worker: %v", startErr)
	}

	waiters := newWaiterHub()
	finalSubs, err := subscribeFinalResults(nc, waiters)
	if err != nil {
		mainLog.Fatalf("subscribe final: %v", err)
	}
	defer func() {
		for _, finalSub := range finalSubs {
			if uerr := finalSub.Unsubscribe(); uerr != nil {
				mainLog.Warnf("final subscription unsubscribe error: %v", uerr)
			}
		}
	}()

	flushErr := nc.Flush()
	if flushErr != nil {
		mainLog.Fatalf("flush: %v", flushErr)
	}

	api := newRuntimeAPI(nc, store, artifacts, waiters, opEvents)
	watcherStarted := startSourceCommitWatcher(ctx, api)
	builderMode, builderModeErr := imageBuilderModeFromEnv()
	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           api.routes(),
		ReadHeaderTimeout: defaultReadHeaderWait,
	}

	logRuntimeStartup(mainLog, natsURL, watcherStarted, builderMode, builderModeErr)

	listenErr := srv.ListenAndServe()
	if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
		mainLog.Fatalf("http server: %v", listenErr)
	}
}

func startPlatformWorkers(
	ctx context.Context,
	natsURL string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
) error {
	workers := []Worker{
		NewRegistrationWorker(natsURL, artifacts, opEvents),
		NewRepoBootstrapWorker(natsURL, artifacts, opEvents),
		NewImageBuilderWorker(natsURL, artifacts, opEvents),
		NewManifestRendererWorker(natsURL, artifacts, opEvents),
		NewDeploymentWorker(natsURL, artifacts, opEvents),
		NewPromotionWorker(natsURL, artifacts, opEvents),
	}
	for _, worker := range workers {
		if err := worker.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

func newRuntimeAPI(
	nc *nats.Conn,
	store *Store,
	artifacts ArtifactStore,
	waiters *waiterHub,
	opEvents *opEventHub,
) *API {
	return &API{
		nc:                  nc,
		store:               store,
		artifacts:           artifacts,
		waiters:             waiters,
		opEvents:            opEvents,
		opHeartbeatInterval: opEventsHeartbeatInterval,
		sourceTriggerMu:     sync.Mutex{},
	}
}

func logRuntimeStartup(
	mainLog sourceLogger,
	natsURL string,
	watcherStarted bool,
	builderMode imageBuilderMode,
	builderModeErr error,
) {
	mainLog.Infof("NATS: %s", natsURL)
	mainLog.Infof("Portal: http://%s", httpAddr)
	mainLog.Infof("Artifacts root: %s", defaultArtifactsRoot)
	mainLog.Infof("Image builder mode: %s", builderMode)
	if builderModeErr != nil {
		mainLog.Warnf("Image builder mode fallback: %v", builderModeErr)
	}
	if watcherStarted {
		mainLog.Infof("Source commit watcher: enabled")
	} else {
		mainLog.Infof("Source commit watcher: disabled (git hooks remain active)")
	}
	mainLog.Infof("Try: create/update/delete projects; delete cleans project artifacts dir")
}
