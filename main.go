package platform

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Run starts the local platform runtime (embedded NATS, workers, and HTTP API).
func Run() {
	mainLog := appLoggerForProcess().Source("main")
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	natsURL, jsDir, jsDirEphemeral, stopNATS := startRuntimeNATS(mainLog)
	defer stopNATS()

	var err error
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
	streamErr := ensureWorkerDeliveryStream(ctx, js)
	if streamErr != nil {
		mainLog.Fatalf("worker delivery stream: %v", streamErr)
	}

	store, err := newStore(ctx, js)
	if err != nil {
		mainLog.Fatalf("store: %v", err)
	}
	opEvents := newOpEventHub(opEventsHistoryLimit, opEventsRetention)
	store.setOpEvents(opEvents)
	runProjectOpsHistoryBackfill(ctx, store, mainLog)

	artifactsRoot := resolveArtifactsRoot()
	artifacts := NewFSArtifacts(artifactsRoot.root)
	mkdirErr := os.MkdirAll(artifactsRoot.root, dirModePrivateRead)
	if mkdirErr != nil {
		mainLog.Fatalf("mkdir artifacts root: %v", mkdirErr)
	}
	builderMode := resolveEffectiveImageBuilderMode(ctx)

	startErr := startPlatformWorkers(ctx, natsURL, artifacts, opEvents, builderMode)
	if startErr != nil {
		mainLog.Fatalf("start worker: %v", startErr)
	}

	waiters := newWaiterHub()
	stopFinalResults, err := subscribeFinalResults(ctx, js, waiters, mainLog)
	if err != nil {
		mainLog.Fatalf("subscribe final: %v", err)
	}
	defer stopFinalResults()

	flushErr := nc.Flush()
	if flushErr != nil {
		mainLog.Fatalf("flush: %v", flushErr)
	}

	api, watcherStarted := newRuntimeAPIWithWatcher(
		ctx,
		nc,
		store,
		artifacts,
		waiters,
		opEvents,
		builderMode,
		artifactsRoot.root,
		jsDir,
		jsDirEphemeral,
	)
	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           api.routes(),
		ReadHeaderTimeout: defaultReadHeaderWait,
	}

	logRuntimeStartup(
		mainLog,
		natsURL,
		jsDir,
		jsDirEphemeral,
		watcherStarted,
		builderMode,
		artifactsRoot,
	)

	serveErr := serveHTTPUntilSignalOrExit(signalCtx, srv, mainLog)
	if serveErr != nil {
		mainLog.Fatalf("http server: %v", serveErr)
	}
}

func serveHTTPUntilSignalOrExit(signalCtx context.Context, srv *http.Server, mainLog sourceLogger) error {
	listenErrCh := make(chan error, 1)
	go func() {
		listenErrCh <- srv.ListenAndServe()
	}()

	select {
	case <-signalCtx.Done():
		mainLog.Infof("Shutdown signal received; draining HTTP server")
		shutdownErr := shutdownHTTPServer(signalCtx, srv, mainLog)
		if shutdownErr != nil {
			return shutdownErr
		}
		listenErr := <-listenErrCh
		if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			return listenErr
		}
	case listenErr := <-listenErrCh:
		if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			return listenErr
		}
	}
	return nil
}

func shutdownHTTPServer(signalCtx context.Context, srv *http.Server, mainLog sourceLogger) error {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(signalCtx), defaultShutdownWait)
	shutdownErr := srv.Shutdown(shutdownCtx)
	shutdownCancel()
	if shutdownErr == nil {
		return nil
	}
	mainLog.Warnf("http graceful shutdown error: %v", shutdownErr)
	if closeErr := srv.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
		mainLog.Warnf("http forced close error: %v", closeErr)
	}
	return shutdownErr
}

func startRuntimeNATS(mainLog sourceLogger) (string, string, bool, func()) {
	ns, natsURL, jsDir, jsDirEphemeral, err := startEmbeddedNATS()
	if err != nil {
		mainLog.Fatalf("start embedded nats: %v", err)
	}
	cleanup := func() {
		ns.Shutdown()
		ns.WaitForShutdown()
		if jsDirEphemeral {
			_ = os.RemoveAll(jsDir)
		}
	}
	return natsURL, jsDir, jsDirEphemeral, cleanup
}

func startPlatformWorkers(
	ctx context.Context,
	natsURL string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
	builderMode imageBuilderModeResolution,
) error {
	workers := []Worker{
		NewRegistrationWorker(natsURL, artifacts, opEvents),
		NewRepoBootstrapWorker(natsURL, artifacts, opEvents),
		NewImageBuilderWorker(natsURL, artifacts, opEvents, builderMode),
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
	builderMode imageBuilderModeResolution,
	artifactsRoot string,
	natsStoreDir string,
	natsStoreEphemeral bool,
) *API {
	return &API{
		nc:                          nc,
		store:                       store,
		artifacts:                   artifacts,
		waiters:                     waiters,
		opEvents:                    opEvents,
		opHeartbeatInterval:         opEventsHeartbeatInterval,
		runtimeVersion:              runtimeBuildVersion(),
		runtimeHTTPAddr:             httpAddr,
		runtimeArtifactsRoot:        strings.TrimSpace(artifactsRoot),
		runtimeBuilderMode:          builderMode,
		runtimeCommitWatcherEnabled: false,
		runtimeNATSEmbedded:         true,
		runtimeNATSStoreDir:         strings.TrimSpace(natsStoreDir),
		runtimeNATSStoreEphemeral:   natsStoreEphemeral,
		sourceTriggerMu:             sync.Mutex{},
		projectStartLocksMu:         sync.Mutex{},
		projectStartLocks:           map[string]*sync.Mutex{},
	}
}

func newRuntimeAPIWithWatcher(
	ctx context.Context,
	nc *nats.Conn,
	store *Store,
	artifacts ArtifactStore,
	waiters *waiterHub,
	opEvents *opEventHub,
	builderMode imageBuilderModeResolution,
	artifactsRoot string,
	natsStoreDir string,
	natsStoreEphemeral bool,
) (*API, bool) {
	api := newRuntimeAPI(
		nc,
		store,
		artifacts,
		waiters,
		opEvents,
		builderMode,
		artifactsRoot,
		natsStoreDir,
		natsStoreEphemeral,
	)
	watcherStarted := startSourceCommitWatcher(ctx, api)
	api.runtimeCommitWatcherEnabled = watcherStarted
	return api, watcherStarted
}

func runtimeBuildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return ""
	}
	return strings.TrimSpace(info.Main.Version)
}

func logRuntimeStartup(
	mainLog sourceLogger,
	natsURL string,
	natsStoreDir string,
	natsStoreEphemeral bool,
	watcherStarted bool,
	builderMode imageBuilderModeResolution,
	artifactsRoot artifactsRootResolution,
) {
	mainLog.Infof("NATS: %s", natsURL)
	if natsStoreEphemeral {
		mainLog.Infof("NATS store dir: %s (ephemeral)", natsStoreDir)
	} else {
		mainLog.Infof("NATS store dir: %s (persistent)", natsStoreDir)
	}
	mainLog.Infof("Portal: http://%s", httpAddr)
	mainLog.Infof("Artifacts root: %s", artifactsRoot.root)
	if shouldLogLegacyArtifactsMigrationNotice(artifactsRoot) {
		mainLog.Warnf(
			"Legacy artifacts root detected at %s while new root is empty. Existing artifacts are not auto-migrated; move files manually or keep the legacy root with %s=%s.",
			artifactsRoot.legacyRoot,
			artifactsRootEnv,
			artifactsRoot.legacyRoot,
		)
	}
	mainLog.Infof(
		"Image builder mode: requested=%s effective=%s explicit=%t",
		builderMode.requestedMode,
		builderMode.effectiveMode,
		builderMode.requestedExplicit,
	)
	if builderMode.requestedWarning != "" {
		mainLog.Warnf("Image builder mode request warning: %s", builderMode.requestedWarning)
	}
	if builderMode.fallbackReason != "" {
		mainLog.Warnf("Image builder mode auto-fallback: %s", builderMode.fallbackReason)
	}
	if builderMode.policyError != "" {
		mainLog.Warnf("Image builder mode policy: %s", builderMode.policyError)
	}
	if watcherStarted {
		mainLog.Infof("Source commit watcher: enabled")
	} else {
		mainLog.Infof("Source commit watcher: disabled (git hooks remain active)")
	}
	mainLog.Infof("Try: create/update/delete projects; delete cleans project artifacts dir")
}

func runProjectOpsHistoryBackfill(
	ctx context.Context,
	store *Store,
	mainLog sourceLogger,
) {
	mainLog.Infof(
		"Project operation history index backfill: start (scan_limit=%d)",
		projectOpsBackfillDefaultScanLimit,
	)
	backfillReport, err := store.backfillProjectOpsIndex(ctx, projectOpsBackfillDefaultScanLimit)
	if err != nil {
		mainLog.Warnf("Project operation history index backfill failed: %v", err)
		return
	}
	logProjectOpsBackfillReport(mainLog, backfillReport)
}

func logProjectOpsBackfillReport(mainLog sourceLogger, report projectOpsBackfillReport) {
	mainLog.Infof(
		"Project operation history index backfill complete: scanned_ops=%d projects_seen=%d projects_updated=%d restored_entries=%d truncated=%t",
		report.ScannedOps,
		report.RebuiltProjects,
		report.UpdatedProjects,
		report.AddedIndexEntries,
		report.Truncated,
	)
	if report.SkippedMalformedOps == 0 &&
		report.SkippedMissingProjectID == 0 &&
		report.SkippedMissingOpID == 0 &&
		report.SkippedReadErrors == 0 {
		return
	}
	mainLog.Warnf(
		"Project operation history index backfill skipped records: malformed=%d missing_project_id=%d missing_op_id=%d read_errors=%d",
		report.SkippedMalformedOps,
		report.SkippedMissingProjectID,
		report.SkippedMissingOpID,
		report.SkippedReadErrors,
	)
}
