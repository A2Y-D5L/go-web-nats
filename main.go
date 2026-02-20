package platform

import (
	"context"
	"errors"
	"net/http"
	"os"

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

	artifacts := NewFSArtifacts(defaultArtifactsRoot)
	mkdirErr := os.MkdirAll(defaultArtifactsRoot, dirModePrivateRead)
	if mkdirErr != nil {
		mainLog.Fatalf("mkdir artifacts root: %v", mkdirErr)
	}

	workers := []Worker{
		NewRegistrationWorker(natsURL, artifacts),
		NewRepoBootstrapWorker(natsURL, artifacts),
		NewImageBuilderWorker(natsURL, artifacts),
		NewManifestRendererWorker(natsURL, artifacts),
	}
	for _, worker := range workers {
		startErr := worker.Start(ctx)
		if startErr != nil {
			mainLog.Fatalf("start worker: %v", startErr)
		}
	}

	waiters := newWaiterHub()
	finalSub, err := subscribeFinalResults(nc, waiters)
	if err != nil {
		mainLog.Fatalf("subscribe final: %v", err)
	}
	defer func() {
		if uerr := finalSub.Unsubscribe(); uerr != nil {
			mainLog.Warnf("final subscription unsubscribe error: %v", uerr)
		}
	}()

	flushErr := nc.Flush()
	if flushErr != nil {
		mainLog.Fatalf("flush: %v", flushErr)
	}

	api := &API{
		nc:        nc,
		store:     store,
		artifacts: artifacts,
		waiters:   waiters,
	}
	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           api.routes(),
		ReadHeaderTimeout: defaultReadHeaderWait,
	}

	mainLog.Infof("NATS: %s", natsURL)
	mainLog.Infof("Portal: http://%s", httpAddr)
	mainLog.Infof("Artifacts root: %s", defaultArtifactsRoot)
	mainLog.Infof("Try: create/update/delete projects; delete cleans project artifacts dir")

	listenErr := srv.ListenAndServe()
	if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
		mainLog.Fatalf("http server: %v", listenErr)
	}
}
