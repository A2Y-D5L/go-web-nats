package platform

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

////////////////////////////////////////////////////////////////////////////////
// Logging
////////////////////////////////////////////////////////////////////////////////

type logLevel string

const (
	logLevelDebug logLevel = "DEBUG"
	logLevelInfo  logLevel = "INFO"
	logLevelWarn  logLevel = "WARN"
	logLevelError logLevel = "ERROR"
)

type appLogger struct {
	base  *log.Logger
	color bool
}

type sourceLogger struct {
	app    *appLogger
	source string
}

func newAppLogger() *appLogger {
	return &appLogger{
		base:  log.New(os.Stdout, "", 0),
		color: supportsColor(),
	}
}

func supportsColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if term == "" || term == "dumb" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func (l *appLogger) Source(source string) sourceLogger {
	return sourceLogger{
		app:    l,
		source: source,
	}
}

func (l *appLogger) logf(level logLevel, source, format string, args ...any) {
	ts := time.Now().UTC().Format(time.RFC3339)
	msg := fmt.Sprintf(format, args...)
	levelText := fmt.Sprintf("%-5s", level)
	sourceText := fmt.Sprintf("%-8s", source)

	if l.color {
		ts = ansi("90", ts)
		levelText = ansi(levelColor(level), levelText)
		sourceText = ansi(sourceColor(source), sourceText)
	}

	l.base.Printf("%s %s %s %s", ts, levelText, sourceText, msg)
}

func (l sourceLogger) Debugf(format string, args ...any) {
	l.app.logf(logLevelDebug, l.source, format, args...)
}

func (l sourceLogger) Infof(format string, args ...any) {
	l.app.logf(logLevelInfo, l.source, format, args...)
}

func (l sourceLogger) Warnf(format string, args ...any) {
	l.app.logf(logLevelWarn, l.source, format, args...)
}

func (l sourceLogger) Errorf(format string, args ...any) {
	l.app.logf(logLevelError, l.source, format, args...)
}

func (l sourceLogger) Fatalf(format string, args ...any) {
	l.app.logf(logLevelError, l.source, format, args...)
	os.Exit(1)
}

func levelColor(level logLevel) string {
	switch level {
	case logLevelDebug:
		return "36" // cyan
	case logLevelInfo:
		return "32" // green
	case logLevelWarn:
		return "33" // yellow
	case logLevelError:
		return "31" // red
	default:
		return "37"
	}
}

func sourceColor(source string) string {
	switch source {
	case branchMain:
		return "97"
	case "api":
		return "94"
	case "registrar":
		return "35"
	case "repoBootstrap":
		return "36"
	case "imageBuilder":
		return "93"
	case "manifestRenderer":
		return "32"
	default:
		palette := []string{"34", "35", "36", "92", "93", "95", "96"}
		h := fnv.New32a()
		_, _ = h.Write([]byte(source))
		return palette[int(h.Sum32())%len(palette)]
	}
}

func ansi(code, s string) string {
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func appLoggerForProcess() *appLogger {
	return newAppLogger()
}

////////////////////////////////////////////////////////////////////////////////
// Entrypoint
////////////////////////////////////////////////////////////////////////////////

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
