// File: main.go
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

////////////////////////////////////////////////////////////////////////////////
// Embedded web UI
////////////////////////////////////////////////////////////////////////////////

//go:embed web/*
var webFS embed.FS

////////////////////////////////////////////////////////////////////////////////
// Subjects (register -> bootstrap -> build -> deploy chain) + KV buckets
////////////////////////////////////////////////////////////////////////////////

const (
	// API publishes project operations here
	subjectProjectOpStart = "paas.project.op.start"

	// Worker pipeline chain
	subjectRegistrationDone = "paas.project.op.registration.done"
	subjectBootstrapDone    = "paas.project.op.bootstrap.done"
	subjectBuildDone        = "paas.project.op.build.done"
	subjectDeployDone       = "paas.project.op.deploy.done"

	// KV buckets
	kvBucketProjects = "paas_projects"
	kvBucketOps      = "paas_ops"

	// Project keys in KV
	kvProjectKeyPrefix = "project/"
	kvOpKeyPrefix      = "op/"

	// HTTP
	httpAddr = "127.0.0.1:8080"

	// Where workers write artifacts
	defaultArtifactsRoot = "./data/artifacts"

	// API wait timeout per request
	apiWaitTimeout = 45 * time.Second

	// Schema defaults (from cfg/project-jsonschema.json)
	projectAPIVersion = "platform.example.com/v2"
	projectKind       = "App"
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
	case "main":
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

var appLog = newAppLogger()

////////////////////////////////////////////////////////////////////////////////
// Domain model: Projects + Operations
////////////////////////////////////////////////////////////////////////////////

type EnvConfig struct {
	Vars map[string]string `json:"vars"`
}

type NetworkPolicies struct {
	Ingress string `json:"ingress"`
	Egress  string `json:"egress"`
}

type ProjectSpec struct {
	APIVersion      string               `json:"apiVersion"`
	Kind            string               `json:"kind"`
	Name            string               `json:"name"`
	Runtime         string               `json:"runtime"`
	Capabilities    []string             `json:"capabilities,omitempty"`
	Environments    map[string]EnvConfig `json:"environments"`
	NetworkPolicies NetworkPolicies      `json:"networkPolicies"`
}

type ProjectStatus struct {
	Phase      string    `json:"phase"`        // Ready | Reconciling | Deleting | Error
	UpdatedAt  time.Time `json:"updated_at"`   //
	LastOpID   string    `json:"last_op_id"`   //
	LastOpKind string    `json:"last_op_kind"` // create|update|delete|ci
	Message    string    `json:"message,omitempty"`
}

type Project struct {
	ID        string        `json:"id"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Spec      ProjectSpec   `json:"spec"`
	Status    ProjectStatus `json:"status"`
}

type OperationKind string

const (
	OpCreate OperationKind = "create"
	OpUpdate OperationKind = "update"
	OpDelete OperationKind = "delete"
	OpCI     OperationKind = "ci"
)

type OpStep struct {
	Worker    string    `json:"worker"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Message   string    `json:"message,omitempty"`
	Error     string    `json:"error,omitempty"`
	Artifacts []string  `json:"artifacts,omitempty"` // relative paths
}

type Operation struct {
	ID        string        `json:"id"`
	Kind      OperationKind `json:"kind"`
	ProjectID string        `json:"project_id"`
	Requested time.Time     `json:"requested"`
	Finished  time.Time     `json:"finished,omitempty"`
	Status    string        `json:"status"` // queued|running|done|error
	Error     string        `json:"error,omitempty"`
	Steps     []OpStep      `json:"steps"`
}

var (
	projectNameRe  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	runtimeRe      = regexp.MustCompile(`^[a-z0-9]+([_-][a-z0-9]+)*(\.[0-9]+(\.[0-9]+)*)?$`)
	capabilityRe   = regexp.MustCompile(`^[a-z][a-z0-9_\-]*[a-z0-9]$`)
	envNameRe      = regexp.MustCompile(`^[a-z][a-z0-9_\-]*[a-z0-9]$`)
	envVarNameRe   = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	networkValueRe = regexp.MustCompile(`^(internal|none)$`)
)

func normalizeProjectSpec(in ProjectSpec) ProjectSpec {
	spec := in
	spec.APIVersion = strings.TrimSpace(spec.APIVersion)
	if spec.APIVersion == "" {
		spec.APIVersion = projectAPIVersion
	}
	spec.Kind = strings.TrimSpace(spec.Kind)
	if spec.Kind == "" {
		spec.Kind = projectKind
	}

	spec.Name = strings.TrimSpace(spec.Name)
	spec.Runtime = strings.TrimSpace(spec.Runtime)

	spec.NetworkPolicies.Ingress = strings.TrimSpace(spec.NetworkPolicies.Ingress)
	spec.NetworkPolicies.Egress = strings.TrimSpace(spec.NetworkPolicies.Egress)
	if spec.NetworkPolicies.Ingress == "" {
		spec.NetworkPolicies.Ingress = "internal"
	}
	if spec.NetworkPolicies.Egress == "" {
		spec.NetworkPolicies.Egress = "internal"
	}

	seenCaps := map[string]struct{}{}
	var caps []string
	for _, c := range spec.Capabilities {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seenCaps[c]; ok {
			continue
		}
		seenCaps[c] = struct{}{}
		caps = append(caps, c)
	}
	spec.Capabilities = caps

	if spec.Environments == nil {
		spec.Environments = map[string]EnvConfig{}
	}
	for envName, envCfg := range spec.Environments {
		if envCfg.Vars == nil {
			envCfg.Vars = map[string]string{}
		}
		spec.Environments[envName] = envCfg
	}
	return spec
}

func validateProjectSpec(spec ProjectSpec) error {
	if spec.APIVersion != projectAPIVersion {
		return fmt.Errorf("apiVersion must be %q", projectAPIVersion)
	}
	if spec.Kind != projectKind {
		return fmt.Errorf("kind must be %q", projectKind)
	}
	if len(spec.Name) < 1 || len(spec.Name) > 63 || !projectNameRe.MatchString(spec.Name) {
		return fmt.Errorf("name must match %s", projectNameRe.String())
	}
	if len(spec.Runtime) < 1 || len(spec.Runtime) > 128 || !runtimeRe.MatchString(spec.Runtime) {
		return fmt.Errorf("runtime must match %s", runtimeRe.String())
	}
	for _, c := range spec.Capabilities {
		if len(c) > 64 || !capabilityRe.MatchString(c) {
			return fmt.Errorf("invalid capability %q", c)
		}
	}
	if len(spec.Environments) < 1 {
		return fmt.Errorf("environments must include at least one environment")
	}
	for envName, envCfg := range spec.Environments {
		if len(envName) > 32 || !envNameRe.MatchString(envName) {
			return fmt.Errorf("invalid environment name %q", envName)
		}
		for k, v := range envCfg.Vars {
			if len(k) > 128 || !envVarNameRe.MatchString(k) {
				return fmt.Errorf("invalid environment variable name %q in %q", k, envName)
			}
			if len(v) > 4096 {
				return fmt.Errorf("env var %q in %q exceeds max length", k, envName)
			}
		}
	}
	if !networkValueRe.MatchString(spec.NetworkPolicies.Ingress) {
		return fmt.Errorf("networkPolicies.ingress must be internal or none")
	}
	if !networkValueRe.MatchString(spec.NetworkPolicies.Egress) {
		return fmt.Errorf("networkPolicies.egress must be internal or none")
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// NATS messages
////////////////////////////////////////////////////////////////////////////////

type ProjectOpMsg struct {
	OpID      string        `json:"op_id"`
	Kind      OperationKind `json:"kind"`
	ProjectID string        `json:"project_id"`
	Spec      ProjectSpec   `json:"spec,omitempty"` // create/update only
	Err       string        `json:"err,omitempty"`
	At        time.Time     `json:"at"`
}

type WorkerResultMsg struct {
	OpID      string        `json:"op_id"`
	Kind      OperationKind `json:"kind"`
	ProjectID string        `json:"project_id"`
	Spec      ProjectSpec   `json:"spec,omitempty"`
	Worker    string        `json:"worker"`
	Message   string        `json:"message,omitempty"`
	Err       string        `json:"err,omitempty"`
	Artifacts []string      `json:"artifacts,omitempty"` // relative paths
	At        time.Time     `json:"at"`
}

////////////////////////////////////////////////////////////////////////////////
// Infrastructure: Embedded NATS + JetStream KV
////////////////////////////////////////////////////////////////////////////////

type KV struct {
	js jetstream.JetStream
}

func (k *KV) Projects(ctx context.Context) (jetstream.KeyValue, error) {
	return ensureKV(ctx, k.js, kvBucketProjects, 25)
}

func (k *KV) Ops(ctx context.Context) (jetstream.KeyValue, error) {
	return ensureKV(ctx, k.js, kvBucketOps, 50)
}

func ensureKV(ctx context.Context, js jetstream.JetStream, bucket string, history uint8) (jetstream.KeyValue, error) {
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:  bucket,
		History: history,
	})
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketExists) {
			return js.KeyValue(ctx, bucket)
		}
		return nil, err
	}
	return kv, nil
}

func startEmbeddedNATS() (*server.Server, string, string, error) {
	storeDir, err := os.MkdirTemp("", "nats-js-*")
	if err != nil {
		return nil, "", "", err
	}
	opts := &server.Options{
		ServerName: "embedded-paas",
		Host:       "127.0.0.1",
		Port:       -1,
		JetStream:  true,
		StoreDir:   storeDir,
		NoSigs:     true,
	}
	ns, err := server.NewServer(opts)
	if err != nil {
		_ = os.RemoveAll(storeDir)
		return nil, "", "", err
	}
	ns.ConfigureLogger()
	ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		ns.Shutdown()
		ns.WaitForShutdown()
		_ = os.RemoveAll(storeDir)
		return nil, "", "", fmt.Errorf("nats not ready")
	}
	return ns, ns.ClientURL(), storeDir, nil
}

////////////////////////////////////////////////////////////////////////////////
// Persistence: Projects + Ops in KV (JSON)
////////////////////////////////////////////////////////////////////////////////

type Store struct {
	kvProjects jetstream.KeyValue
	kvOps      jetstream.KeyValue
}

func newStore(ctx context.Context, kv *KV) (*Store, error) {
	p, err := kv.Projects(ctx)
	if err != nil {
		return nil, err
	}
	o, err := kv.Ops(ctx)
	if err != nil {
		return nil, err
	}
	return &Store{kvProjects: p, kvOps: o}, nil
}

func (s *Store) PutProject(ctx context.Context, p Project) error {
	p.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.kvProjects.Put(ctx, kvProjectKeyPrefix+p.ID, b)
	return err
}

func (s *Store) GetProject(ctx context.Context, projectID string) (Project, error) {
	e, err := s.kvProjects.Get(ctx, kvProjectKeyPrefix+projectID)
	if err != nil {
		return Project{}, err
	}
	var p Project
	if err := json.Unmarshal(e.Value(), &p); err != nil {
		return Project{}, err
	}
	return p, nil
}

func (s *Store) DeleteProject(ctx context.Context, projectID string) error {
	return s.kvProjects.Delete(ctx, kvProjectKeyPrefix+projectID)
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	keys, err := s.kvProjects.Keys(ctx)
	if err != nil {
		// Some KV backends can return ErrNoKeys if empty; treat as empty.
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return []Project{}, nil
		}
		return nil, err
	}
	var out []Project
	for _, k := range keys {
		if !strings.HasPrefix(k, kvProjectKeyPrefix) {
			continue
		}
		projectID := strings.TrimPrefix(k, kvProjectKeyPrefix)
		p, err := s.GetProject(ctx, projectID)
		if err != nil {
			// best-effort listing
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) PutOp(ctx context.Context, op Operation) error {
	b, err := json.Marshal(op)
	if err != nil {
		return err
	}
	_, err = s.kvOps.Put(ctx, kvOpKeyPrefix+op.ID, b)
	return err
}

func (s *Store) GetOp(ctx context.Context, opID string) (Operation, error) {
	e, err := s.kvOps.Get(ctx, kvOpKeyPrefix+opID)
	if err != nil {
		return Operation{}, err
	}
	var op Operation
	if err := json.Unmarshal(e.Value(), &op); err != nil {
		return Operation{}, err
	}
	return op, nil
}

////////////////////////////////////////////////////////////////////////////////
// Artifact store (disk)
////////////////////////////////////////////////////////////////////////////////

type ArtifactStore interface {
	ProjectDir(projectID string) string
	EnsureProjectDir(projectID string) (string, error)
	WriteFile(projectID, relPath string, data []byte) (string, error) // returns relative path
	ListFiles(projectID string) ([]string, error)                     // returns relative paths
	ReadFile(projectID, relPath string) ([]byte, error)
	RemoveProject(projectID string) error
}

type FSArtifacts struct {
	root string
}

func NewFSArtifacts(root string) *FSArtifacts {
	return &FSArtifacts{root: root}
}

func (a *FSArtifacts) ProjectDir(projectID string) string {
	return filepath.Join(a.root, projectID)
}

func (a *FSArtifacts) EnsureProjectDir(projectID string) (string, error) {
	dir := a.ProjectDir(projectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (a *FSArtifacts) WriteFile(projectID, relPath string, data []byte) (string, error) {
	dir, err := a.EnsureProjectDir(projectID)
	if err != nil {
		return "", err
	}
	relPath = filepath.Clean(relPath)
	if strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
		return "", fmt.Errorf("invalid relPath")
	}
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(relPath), nil
}

func (a *FSArtifacts) ListFiles(projectID string) ([]string, error) {
	root := a.ProjectDir(projectID)
	var files []string
	_, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func (a *FSArtifacts) ReadFile(projectID, relPath string) ([]byte, error) {
	dir := a.ProjectDir(projectID)
	relPath = filepath.Clean(relPath)
	if strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
		return nil, fmt.Errorf("invalid relPath")
	}
	full := filepath.Join(dir, relPath)
	return os.ReadFile(full)
}

func (a *FSArtifacts) RemoveProject(projectID string) error {
	return os.RemoveAll(a.ProjectDir(projectID))
}

////////////////////////////////////////////////////////////////////////////////
// Wait hub for API: wait for final worker result by op_id
////////////////////////////////////////////////////////////////////////////////

type waiterHub struct {
	mu      sync.Mutex
	waiters map[string]chan WorkerResultMsg
}

func newWaiterHub() *waiterHub {
	return &waiterHub{waiters: map[string]chan WorkerResultMsg{}}
}

func (h *waiterHub) register(opID string) <-chan WorkerResultMsg {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan WorkerResultMsg, 1)
	h.waiters[opID] = ch
	return ch
}

func (h *waiterHub) unregister(opID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.waiters, opID)
}

func (h *waiterHub) deliver(opID string, msg WorkerResultMsg) {
	h.mu.Lock()
	ch, ok := h.waiters[opID]
	h.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

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

func newWorkerBase(name, natsURL, subjectIn, subjectOut string, artifacts ArtifactStore) WorkerBase {
	return WorkerBase{
		name:       name,
		natsURL:    natsURL,
		subjectIn:  subjectIn,
		subjectOut: subjectOut,
		artifacts:  artifacts,
	}
}

func (b WorkerBase) connect() (*nats.Conn, jetstream.JetStream, *Store, error) {
	nc, err := nats.Connect(b.natsURL, nats.Name(b.name))
	if err != nil {
		return nil, nil, nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, nil, err
	}
	kv := &KV{js: js}
	store, err := newStore(context.Background(), kv)
	if err != nil {
		nc.Close()
		return nil, nil, nil, err
	}
	return nc, js, store, nil
}

type RegistrationWorker struct{ WorkerBase }
type RepoBootstrapWorker struct{ WorkerBase }
type ImageBuilderWorker struct{ WorkerBase }
type ManifestRendererWorker struct{ WorkerBase }

func NewRegistrationWorker(natsURL string, artifacts ArtifactStore) *RegistrationWorker {
	return &RegistrationWorker{WorkerBase: newWorkerBase("registrar", natsURL, subjectProjectOpStart, subjectRegistrationDone, artifacts)}
}
func NewRepoBootstrapWorker(natsURL string, artifacts ArtifactStore) *RepoBootstrapWorker {
	return &RepoBootstrapWorker{WorkerBase: newWorkerBase("repoBootstrap", natsURL, subjectRegistrationDone, subjectBootstrapDone, artifacts)}
}
func NewImageBuilderWorker(natsURL string, artifacts ArtifactStore) *ImageBuilderWorker {
	return &ImageBuilderWorker{WorkerBase: newWorkerBase("imageBuilder", natsURL, subjectBootstrapDone, subjectBuildDone, artifacts)}
}
func NewManifestRendererWorker(natsURL string, artifacts ArtifactStore) *ManifestRendererWorker {
	return &ManifestRendererWorker{WorkerBase: newWorkerBase("manifestRenderer", natsURL, subjectBuildDone, subjectDeployDone, artifacts)}
}

func (w *RegistrationWorker) Start(ctx context.Context) error {
	return startWorker(ctx, w.name, w.natsURL, w.subjectIn, w.subjectOut, w.artifacts, registrationWorkerAction)
}
func (w *RepoBootstrapWorker) Start(ctx context.Context) error {
	return startWorker(ctx, w.name, w.natsURL, w.subjectIn, w.subjectOut, w.artifacts, repoBootstrapWorkerAction)
}
func (w *ImageBuilderWorker) Start(ctx context.Context) error {
	return startWorker(ctx, w.name, w.natsURL, w.subjectIn, w.subjectOut, w.artifacts, imageBuilderWorkerAction)
}
func (w *ManifestRendererWorker) Start(ctx context.Context) error {
	return startWorker(ctx, w.name, w.natsURL, w.subjectIn, w.subjectOut, w.artifacts, manifestRendererWorkerAction)
}

type workerFn func(ctx context.Context, store *Store, artifacts ArtifactStore, msg ProjectOpMsg) (WorkerResultMsg, error)

// startWorker subscribes to one subject (unique per worker), does work, and publishes a result for the next worker.
func startWorker(ctx context.Context, workerName, natsURL, inSubj, outSubj string, artifacts ArtifactStore, fn workerFn) error {
	workerLog := appLog.Source(workerName)

	go func() {
		nc, err := nats.Connect(natsURL, nats.Name(workerName))
		if err != nil {
			workerLog.Errorf("connect error: %v", err)
			return
		}
		defer nc.Drain()

		js, err := jetstream.New(nc)
		if err != nil {
			workerLog.Errorf("jetstream error: %v", err)
			return
		}
		store, err := newStore(context.Background(), &KV{js: js})
		if err != nil {
			workerLog.Errorf("store error: %v", err)
			return
		}
		workerLog.Infof("ready: subscribe=%s publish=%s", inSubj, outSubj)

		sub, err := nc.Subscribe(inSubj, func(m *nats.Msg) {
			var opMsg ProjectOpMsg
			if err := json.Unmarshal(m.Data, &opMsg); err != nil {
				workerLog.Warnf("discarding invalid message on %s: %v", inSubj, err)
				return
			}
			if opMsg.Err != "" {
				workerLog.Warnf("skip op=%s due to upstream error: %s", opMsg.OpID, opMsg.Err)
				res := WorkerResultMsg{
					OpID:      opMsg.OpID,
					Kind:      opMsg.Kind,
					ProjectID: opMsg.ProjectID,
					Spec:      opMsg.Spec,
					Worker:    workerName,
					Err:       opMsg.Err,
					Message:   "skipped due to upstream error",
					At:        time.Now().UTC(),
				}
				b, _ := json.Marshal(res)
				_ = nc.Publish(outSubj, b)
				return
			}
			workerLog.Infof("start op=%s kind=%s project=%s", opMsg.OpID, opMsg.Kind, opMsg.ProjectID)

			// Execute worker function
			res, werr := fn(context.Background(), store, artifacts, opMsg)
			if werr != nil {
				res.Err = werr.Error()
				workerLog.Errorf("op=%s failed: %v", opMsg.OpID, werr)
			} else {
				workerLog.Infof("done op=%s message=%q artifacts=%d", opMsg.OpID, res.Message, len(res.Artifacts))
			}
			res.Worker = workerName
			res.OpID = opMsg.OpID
			res.Kind = opMsg.Kind
			res.ProjectID = opMsg.ProjectID
			res.Spec = opMsg.Spec
			if res.Err == "" {
				res.Err = opMsg.Err
			}
			res.At = time.Now().UTC()

			b, _ := json.Marshal(res)
			if err := nc.Publish(outSubj, b); err != nil {
				workerLog.Errorf("publish result failed op=%s subject=%s: %v", opMsg.OpID, outSubj, err)
			}
		})
		if err != nil {
			workerLog.Errorf("subscribe error: %v", err)
			return
		}
		defer sub.Unsubscribe()

		_ = nc.Flush()
		<-ctx.Done()
	}()

	return nil
}

////////////////////////////////////////////////////////////////////////////////
// Worker actions: real-world-ish PaaS artifacts
////////////////////////////////////////////////////////////////////////////////

func registrationWorkerAction(ctx context.Context, store *Store, artifacts ArtifactStore, msg ProjectOpMsg) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := WorkerResultMsg{Message: "registration worker starting"}
	_ = markOpStepStart(ctx, store, msg.OpID, "registrar", stepStart, "register app configuration")

	spec := normalizeProjectSpec(msg.Spec)

	switch msg.Kind {
	case OpCreate, OpUpdate:
		if err := validateProjectSpec(spec); err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "registrar", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		_, _ = artifacts.EnsureProjectDir(msg.ProjectID)
		a1, err := artifacts.WriteFile(msg.ProjectID, "registration/project.yaml", renderProjectConfigYAML(spec))
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "registrar", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		a2, err := artifacts.WriteFile(msg.ProjectID, "registration/registration.json", mustJSON(map[string]any{
			"project_id": msg.ProjectID,
			"op_id":      msg.OpID,
			"kind":       msg.Kind,
			"registered": time.Now().UTC(),
			"name":       spec.Name,
			"runtime":    spec.Runtime,
		}))
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "registrar", time.Now().UTC(), "", err.Error(), []string{a1})
			return res, err
		}
		res.Message = "project registration upserted"
		res.Artifacts = []string{a1, a2}

	case OpDelete:
		a1, err := artifacts.WriteFile(msg.ProjectID, "registration/deregister.txt",
			[]byte(fmt.Sprintf("deregister requested at %s\nop=%s\n", time.Now().UTC().Format(time.RFC3339), msg.OpID)))
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "registrar", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		res.Message = "project deregistration staged"
		res.Artifacts = []string{a1}

	default:
		err := fmt.Errorf("unknown op kind: %s", msg.Kind)
		_ = markOpStepEnd(ctx, store, msg.OpID, "registrar", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}

	_ = markOpStepEnd(ctx, store, msg.OpID, "registrar", time.Now().UTC(), res.Message, "", res.Artifacts)
	return res, nil
}

func localAPIBaseURL() string {
	base := strings.TrimSpace(os.Getenv("PAAS_LOCAL_API_BASE_URL"))
	if base == "" {
		base = "http://" + httpAddr
	}
	return strings.TrimRight(base, "/")
}

func sourceWebhookEndpoint() string {
	return localAPIBaseURL() + "/api/webhooks/source"
}

func sourceRepoDir(artifacts ArtifactStore, projectID string) string {
	return filepath.Join(artifacts.ProjectDir(projectID), "repos", "source")
}

func manifestsRepoDir(artifacts ArtifactStore, projectID string) string {
	return filepath.Join(artifacts.ProjectDir(projectID), "repos", "manifests")
}

func runCmd(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func gitHasStagedChanges(dir string) (bool, error) {
	cmd := exec.Command("git", "diff", "--cached", "--quiet", "--exit-code")
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git diff --cached --quiet: %w", err)
}

func gitCommitIfChanged(dir, message string) (bool, error) {
	if err := runCmd(dir, nil, "git", "add", "-A"); err != nil {
		return false, err
	}
	changed, err := gitHasStagedChanges(dir)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := runCmd(dir, nil, "git", "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

func gitRevParse(dir, ref string) (string, error) {
	cmd := exec.Command("git", "rev-parse", ref)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("git rev-parse %s: %w: %s", ref, err, msg)
		}
		return "", fmt.Errorf("git rev-parse %s: %w", ref, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ensureLocalGitRepo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := runCmd(dir, nil, "git", "init", "-b", "main"); err != nil {
			// Fallback for older git versions that do not support `-b`.
			if err2 := runCmd(dir, nil, "git", "init"); err2 != nil {
				return fmt.Errorf("git init failed: %v; fallback failed: %w", err, err2)
			}
		}
	}
	if err := runCmd(dir, nil, "git", "checkout", "-B", "main"); err != nil {
		return err
	}
	if err := runCmd(dir, nil, "git", "config", "user.name", "Local PaaS Bot"); err != nil {
		return err
	}
	if err := runCmd(dir, nil, "git", "config", "user.email", "paas-local@example.invalid"); err != nil {
		return err
	}
	if err := runCmd(dir, nil, "git", "config", "commit.gpgsign", "false"); err != nil {
		return err
	}
	return nil
}

func writeFileIfMissing(path string, data []byte, mode os.FileMode) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return false, err
	}
	return true, nil
}

func upsertFile(path string, data []byte, mode os.FileMode) (bool, error) {
	prev, err := os.ReadFile(path)
	if err == nil && string(prev) == string(data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return false, err
	}
	return true, nil
}

func relPath(baseDir, fullPath string) string {
	rel, err := filepath.Rel(baseDir, fullPath)
	if err != nil {
		return filepath.ToSlash(fullPath)
	}
	return filepath.ToSlash(rel)
}

func renderSourceWebhookHookScript(projectID, endpoint string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu

if ! command -v git >/dev/null 2>&1; then
  exit 0
fi
if ! command -v curl >/dev/null 2>&1; then
  exit 0
fi

branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
if [ "$branch" != "main" ]; then
  exit 0
fi

msg="$(git log -1 --pretty=%%s 2>/dev/null || true)"
case "$msg" in
  platform-sync:*)
    exit 0
    ;;
esac

commit="$(git rev-parse HEAD 2>/dev/null || true)"
if [ -z "$commit" ]; then
  exit 0
fi

curl -fsS --max-time 2 \
  -H 'Content-Type: application/json' \
  -X POST '%s' \
  -d "{\"project_id\":\"%s\",\"repo\":\"source\",\"branch\":\"${branch}\",\"ref\":\"refs/heads/${branch}\",\"commit\":\"${commit}\"}" \
  >/dev/null || true
`, endpoint, projectID)
}

func installSourceWebhookHooks(repoDir, projectID, endpoint string) error {
	script := []byte(renderSourceWebhookHookScript(projectID, endpoint))
	for _, hook := range []string{"post-commit", "post-merge"} {
		hookPath := filepath.Join(repoDir, ".git", "hooks", hook)
		if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(hookPath, script, 0o755); err != nil {
			return err
		}
		if err := os.Chmod(hookPath, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		set[filepath.ToSlash(v)] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func repoBootstrapWorkerAction(ctx context.Context, store *Store, artifacts ArtifactStore, msg ProjectOpMsg) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := WorkerResultMsg{Message: "repo bootstrap worker starting"}
	_ = markOpStepStart(ctx, store, msg.OpID, "repoBootstrap", stepStart, "bootstrap source and manifests repos")

	spec := normalizeProjectSpec(msg.Spec)
	switch msg.Kind {
	case OpCreate, OpUpdate:
		projectDir, err := artifacts.EnsureProjectDir(msg.ProjectID)
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		sourceDir := sourceRepoDir(artifacts, msg.ProjectID)
		manifestsDir := manifestsRepoDir(artifacts, msg.ProjectID)
		if err := ensureLocalGitRepo(sourceDir); err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		if err := ensureLocalGitRepo(manifestsDir); err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}

		var touched []string
		sourceReadme := filepath.Join(sourceDir, "README.md")
		created, err := writeFileIfMissing(sourceReadme, []byte(fmt.Sprintf("# %s source\n\nRuntime: %s\n", spec.Name, spec.Runtime)), 0o644)
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}
		if created {
			touched = append(touched, relPath(projectDir, sourceReadme))
		}

		sourceMain := filepath.Join(sourceDir, "main.go")
		created, err = writeFileIfMissing(sourceMain, []byte(fmt.Sprintf(`package main

import "fmt"

func main() { fmt.Println("hello from %s") }
`, spec.Name)), 0o644)
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}
		if created {
			touched = append(touched, relPath(projectDir, sourceMain))
		}

		manifestsReadme := filepath.Join(manifestsDir, "README.md")
		created, err = writeFileIfMissing(manifestsReadme, []byte(fmt.Sprintf("# %s manifests\n\nTarget image: local/%s:latest\n", spec.Name, safeName(spec.Name))), 0o644)
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}
		if created {
			touched = append(touched, relPath(projectDir, manifestsReadme))
		}

		sourceRepoMeta := filepath.Join(sourceDir, ".paas", "repo.json")
		updated, err := upsertFile(sourceRepoMeta, mustJSON(map[string]any{
			"project_id": msg.ProjectID,
			"repo":       "source",
			"path":       sourceDir,
			"branch":     "main",
		}), 0o644)
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}
		if updated {
			touched = append(touched, relPath(projectDir, sourceRepoMeta))
		}

		manifestsRepoMeta := filepath.Join(manifestsDir, ".paas", "repo.json")
		updated, err = upsertFile(manifestsRepoMeta, mustJSON(map[string]any{
			"project_id": msg.ProjectID,
			"repo":       "manifests",
			"path":       manifestsDir,
			"branch":     "main",
		}), 0o644)
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}
		if updated {
			touched = append(touched, relPath(projectDir, manifestsRepoMeta))
		}

		if _, err := gitCommitIfChanged(sourceDir, fmt.Sprintf("platform-sync: bootstrap source repo (%s)", shortID(msg.OpID))); err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}
		if _, err := gitCommitIfChanged(manifestsDir, fmt.Sprintf("platform-sync: bootstrap manifests repo (%s)", shortID(msg.OpID))); err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}

		webhookURL := sourceWebhookEndpoint()
		if err := installSourceWebhookHooks(sourceDir, msg.ProjectID, webhookURL); err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}
		webhookMeta := filepath.Join(sourceDir, ".paas", "webhook.json")
		updated, err = upsertFile(webhookMeta, mustJSON(map[string]any{
			"project_id": msg.ProjectID,
			"repo":       "source",
			"branch":     "main",
			"endpoint":   webhookURL,
			"hooks":      []string{"post-commit", "post-merge"},
		}), 0o644)
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}
		if updated {
			touched = append(touched, relPath(projectDir, webhookMeta))
		}
		if _, err := gitCommitIfChanged(sourceDir, fmt.Sprintf("platform-sync: configure source webhook (%s)", shortID(msg.OpID))); err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}

		sourceHead, _ := gitRevParse(sourceDir, "HEAD")
		manifestsHead, _ := gitRevParse(manifestsDir, "HEAD")
		bootstrapInfo := filepath.Join(projectDir, "repos", "bootstrap-local.json")
		updated, err = upsertFile(bootstrapInfo, mustJSON(map[string]any{
			"project_id":         msg.ProjectID,
			"source_repo_path":   sourceDir,
			"source_branch":      "main",
			"source_head":        sourceHead,
			"manifests_repo":     manifestsDir,
			"manifests_branch":   "main",
			"manifests_head":     manifestsHead,
			"webhook_endpoint":   webhookURL,
			"webhook_event_repo": "source",
		}), 0o644)
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), touched)
			return res, err
		}
		if updated {
			touched = append(touched, relPath(projectDir, bootstrapInfo))
		}

		res.Message = "bootstrapped local source/manifests git repos and installed source webhook"
		res.Artifacts = uniqueSorted(touched)

	case OpDelete:
		a1, err := artifacts.WriteFile(msg.ProjectID, "repos/teardown-plan.txt",
			[]byte("archive source repo\narchive manifests repo\nremove project workspace\n"))
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		res.Message = "repository teardown plan generated"
		res.Artifacts = []string{a1}

	default:
		err := fmt.Errorf("unknown op kind: %s", msg.Kind)
		_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}

	_ = markOpStepEnd(ctx, store, msg.OpID, "repoBootstrap", time.Now().UTC(), res.Message, "", res.Artifacts)
	return res, nil
}

func imageBuilderWorkerAction(ctx context.Context, store *Store, artifacts ArtifactStore, msg ProjectOpMsg) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := WorkerResultMsg{Message: "image builder worker starting"}
	_ = markOpStepStart(ctx, store, msg.OpID, "imageBuilder", stepStart, "build and publish image to local daemon")

	spec := normalizeProjectSpec(msg.Spec)
	imageTag := fmt.Sprintf("local/%s:%s", safeName(spec.Name), shortID(msg.OpID))

	switch msg.Kind {
	case OpCreate, OpUpdate, OpCI:
		a1, err := artifacts.WriteFile(msg.ProjectID, "build/Dockerfile", []byte(fmt.Sprintf(`FROM alpine:3.20
WORKDIR /app
COPY . .
CMD ["sh", "-c", "echo running %s (%s) && sleep infinity"]
`, spec.Name, spec.Runtime)))
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "imageBuilder", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		a2, err := artifacts.WriteFile(msg.ProjectID, "build/publish-local-daemon.json", mustJSON(map[string]any{
			"op_id":         msg.OpID,
			"project_id":    msg.ProjectID,
			"image":         imageTag,
			"runtime":       spec.Runtime,
			"published_at":  time.Now().UTC().Format(time.RFC3339),
			"daemon_target": "local",
		}))
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "imageBuilder", time.Now().UTC(), "", err.Error(), []string{a1})
			return res, err
		}
		a3, err := artifacts.WriteFile(msg.ProjectID, "build/image.txt", []byte(imageTag+"\n"))
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "imageBuilder", time.Now().UTC(), "", err.Error(), []string{a1, a2})
			return res, err
		}
		res.Message = "container image built and published to local daemon"
		res.Artifacts = []string{a1, a2, a3}

	case OpDelete:
		a1, err := artifacts.WriteFile(msg.ProjectID, "build/image-prune.txt", []byte(fmt.Sprintf("prune local image for project=%s op=%s\n", msg.ProjectID, msg.OpID)))
		if err != nil {
			_ = markOpStepEnd(ctx, store, msg.OpID, "imageBuilder", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		res.Message = "container prune plan generated"
		res.Artifacts = []string{a1}

	default:
		err := fmt.Errorf("unknown op kind: %s", msg.Kind)
		_ = markOpStepEnd(ctx, store, msg.OpID, "imageBuilder", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}

	_ = markOpStepEnd(ctx, store, msg.OpID, "imageBuilder", time.Now().UTC(), res.Message, "", res.Artifacts)
	return res, nil
}

func manifestRendererWorkerAction(ctx context.Context, store *Store, artifacts ArtifactStore, msg ProjectOpMsg) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := WorkerResultMsg{Message: "manifest renderer worker starting"}
	_ = markOpStepStart(ctx, store, msg.OpID, "manifestRenderer", stepStart, "render kubernetes deployment manifests")

	spec := normalizeProjectSpec(msg.Spec)
	imageTag := fmt.Sprintf("local/%s:%s", safeName(spec.Name), shortID(msg.OpID))

	switch msg.Kind {
	case OpCreate, OpUpdate, OpCI:
		deployment := renderDeploymentManifest(spec, imageTag)
		service := renderServiceManifest(spec)

		a1, err := artifacts.WriteFile(msg.ProjectID, "deploy/deployment.yaml", []byte(deployment))
		if err != nil {
			_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
			_ = markOpStepEnd(ctx, store, msg.OpID, "manifestRenderer", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		a2, err := artifacts.WriteFile(msg.ProjectID, "deploy/service.yaml", []byte(service))
		if err != nil {
			_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
			_ = markOpStepEnd(ctx, store, msg.OpID, "manifestRenderer", time.Now().UTC(), "", err.Error(), []string{a1})
			return res, err
		}
		a3, err := artifacts.WriteFile(msg.ProjectID, "repos/manifests/deployment.yaml", []byte(deployment))
		if err != nil {
			_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
			_ = markOpStepEnd(ctx, store, msg.OpID, "manifestRenderer", time.Now().UTC(), "", err.Error(), []string{a1, a2})
			return res, err
		}
		a4, err := artifacts.WriteFile(msg.ProjectID, "repos/manifests/service.yaml", []byte(service))
		if err != nil {
			_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
			_ = markOpStepEnd(ctx, store, msg.OpID, "manifestRenderer", time.Now().UTC(), "", err.Error(), []string{a1, a2, a3})
			return res, err
		}
		manifestsDir := manifestsRepoDir(artifacts, msg.ProjectID)
		if err := ensureLocalGitRepo(manifestsDir); err != nil {
			_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
			_ = markOpStepEnd(ctx, store, msg.OpID, "manifestRenderer", time.Now().UTC(), "", err.Error(), []string{a1, a2, a3, a4})
			return res, err
		}
		if _, err := gitCommitIfChanged(manifestsDir, fmt.Sprintf("platform-sync: render manifests (%s)", shortID(msg.OpID))); err != nil {
			_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
			_ = markOpStepEnd(ctx, store, msg.OpID, "manifestRenderer", time.Now().UTC(), "", err.Error(), []string{a1, a2, a3, a4})
			return res, err
		}

		if p, err := store.GetProject(ctx, msg.ProjectID); err == nil {
			p.Spec = spec
			p.Status = ProjectStatus{
				Phase:      "Ready",
				UpdatedAt:  time.Now().UTC(),
				LastOpID:   msg.OpID,
				LastOpKind: string(msg.Kind),
				Message:    "ready",
			}
			_ = store.PutProject(ctx, p)
		}

		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "done", "")
		res.Message = "rendered kubernetes deployment manifests"
		res.Artifacts = []string{a1, a2, a3, a4}

	case OpDelete:
		auditDir := filepath.Join(filepath.Dir(artifacts.ProjectDir(msg.ProjectID)), "_audit")
		_ = os.MkdirAll(auditDir, 0o755)
		_ = os.WriteFile(filepath.Join(auditDir, fmt.Sprintf("%s.deleted.txt", msg.ProjectID)),
			[]byte(fmt.Sprintf("project=%s deleted at %s op=%s\n", msg.ProjectID, time.Now().UTC().Format(time.RFC3339), msg.OpID)),
			0o644,
		)

		if err := artifacts.RemoveProject(msg.ProjectID); err != nil {
			_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
			_ = markOpStepEnd(ctx, store, msg.OpID, "manifestRenderer", time.Now().UTC(), "", err.Error(), nil)
			return res, err
		}
		_ = store.DeleteProject(ctx, msg.ProjectID)
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "done", "")
		res.Message = "project deleted and artifacts cleaned"
		res.Artifacts = []string{}

	default:
		err := fmt.Errorf("unknown op kind: %s", msg.Kind)
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(ctx, store, msg.OpID, "manifestRenderer", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}

	_ = markOpStepEnd(ctx, store, msg.OpID, "manifestRenderer", time.Now().UTC(), res.Message, "", res.Artifacts)
	return res, nil
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func yamlQuoted(v string) string {
	return fmt.Sprintf("%q", v)
}

func renderProjectConfigYAML(spec ProjectSpec) []byte {
	spec = normalizeProjectSpec(spec)
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: %s\n", spec.APIVersion)
	fmt.Fprintf(&b, "kind: %s\n", spec.Kind)
	fmt.Fprintf(&b, "name: %s\n", spec.Name)
	fmt.Fprintf(&b, "runtime: %s\n", spec.Runtime)
	if len(spec.Capabilities) > 0 {
		b.WriteString("capabilities:\n")
		for _, c := range spec.Capabilities {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
	}
	b.WriteString("environments:\n")
	for _, env := range sortedKeys(spec.Environments) {
		cfg := spec.Environments[env]
		fmt.Fprintf(&b, "  %s:\n", env)
		b.WriteString("    vars:\n")
		keys := sortedKeys(cfg.Vars)
		if len(keys) == 0 {
			b.WriteString("      {}\n")
		}
		for _, k := range keys {
			fmt.Fprintf(&b, "      %s: %s\n", k, yamlQuoted(cfg.Vars[k]))
		}
	}
	b.WriteString("networkPolicies:\n")
	fmt.Fprintf(&b, "  ingress: %s\n", spec.NetworkPolicies.Ingress)
	fmt.Fprintf(&b, "  egress: %s\n", spec.NetworkPolicies.Egress)
	return []byte(b.String())
}

func preferredEnvironment(spec ProjectSpec) (string, map[string]string) {
	spec = normalizeProjectSpec(spec)
	if env, ok := spec.Environments["dev"]; ok {
		return "dev", env.Vars
	}
	names := sortedKeys(spec.Environments)
	if len(names) == 0 {
		return "default", map[string]string{}
	}
	first := names[0]
	return string(first), spec.Environments[first].Vars
}

func renderDeploymentManifest(spec ProjectSpec, image string) string {
	spec = normalizeProjectSpec(spec)
	envName, vars := preferredEnvironment(spec)
	name := safeName(spec.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: apps/v1\n")
	fmt.Fprintf(&b, "kind: Deployment\n")
	fmt.Fprintf(&b, "metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", name)
	fmt.Fprintf(&b, "spec:\n")
	fmt.Fprintf(&b, "  replicas: 1\n")
	fmt.Fprintf(&b, "  selector:\n")
	fmt.Fprintf(&b, "    matchLabels:\n")
	fmt.Fprintf(&b, "      app: %s\n", name)
	fmt.Fprintf(&b, "  template:\n")
	fmt.Fprintf(&b, "    metadata:\n")
	fmt.Fprintf(&b, "      labels:\n")
	fmt.Fprintf(&b, "        app: %s\n", name)
	fmt.Fprintf(&b, "      annotations:\n")
	fmt.Fprintf(&b, "        platform.example.com/environment: %s\n", envName)
	fmt.Fprintf(&b, "        platform.example.com/ingress: %s\n", spec.NetworkPolicies.Ingress)
	fmt.Fprintf(&b, "        platform.example.com/egress: %s\n", spec.NetworkPolicies.Egress)
	fmt.Fprintf(&b, "    spec:\n")
	fmt.Fprintf(&b, "      containers:\n")
	fmt.Fprintf(&b, "      - name: app\n")
	fmt.Fprintf(&b, "        image: %s\n", image)
	fmt.Fprintf(&b, "        imagePullPolicy: IfNotPresent\n")
	fmt.Fprintf(&b, "        ports:\n")
	fmt.Fprintf(&b, "        - containerPort: 8080\n")
	keys := sortedKeys(vars)
	if len(keys) > 0 {
		fmt.Fprintf(&b, "        env:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "        - name: %s\n", k)
			fmt.Fprintf(&b, "          value: %s\n", yamlQuoted(vars[k]))
		}
	}
	return b.String()
}

func renderServiceManifest(spec ProjectSpec) string {
	spec = normalizeProjectSpec(spec)
	name := safeName(spec.Name)
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
spec:
  selector:
    app: %s
  ports:
  - name: http
    port: 80
    targetPort: 8080
`, name, name)
}

func safeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "project"
	}
	var out []rune
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-' || r == '_':
			out = append(out, '-')
		case r == ' ':
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "project"
	}
	return string(out)
}

////////////////////////////////////////////////////////////////////////////////
// Operation bookkeeping helpers
////////////////////////////////////////////////////////////////////////////////

func markOpStepStart(ctx context.Context, store *Store, opID, worker string, startedAt time.Time, msg string) error {
	op, err := store.GetOp(ctx, opID)
	if err != nil {
		return err
	}
	op.Status = "running"
	op.Steps = append(op.Steps, OpStep{
		Worker:    worker,
		StartedAt: startedAt,
		Message:   msg,
	})
	return store.PutOp(ctx, op)
}

func markOpStepEnd(ctx context.Context, store *Store, opID, worker string, endedAt time.Time, message, stepErr string, artifacts []string) error {
	op, err := store.GetOp(ctx, opID)
	if err != nil {
		return err
	}
	// Find last step for worker that doesn't have EndedAt set.
	for i := len(op.Steps) - 1; i >= 0; i-- {
		if op.Steps[i].Worker == worker && op.Steps[i].EndedAt.IsZero() {
			op.Steps[i].EndedAt = endedAt
			if message != "" {
				op.Steps[i].Message = message
			}
			op.Steps[i].Error = stepErr
			op.Steps[i].Artifacts = artifacts
			break
		}
	}
	if stepErr != "" {
		op.Status = "error"
		op.Error = stepErr
		op.Finished = time.Now().UTC()
	}
	return store.PutOp(ctx, op)
}

func finalizeOp(ctx context.Context, store *Store, opID, projectID string, kind OperationKind, status, errMsg string) error {
	op, err := store.GetOp(ctx, opID)
	if err != nil {
		return err
	}
	op.Status = status
	op.Error = errMsg
	op.Finished = time.Now().UTC()
	if err := store.PutOp(ctx, op); err != nil {
		return err
	}

	// Best-effort: update project status (except delete where record might be removed later)
	p, err := store.GetProject(ctx, projectID)
	if err != nil {
		return nil
	}
	switch {
	case kind == OpDelete && status == "running":
		p.Status.Phase = "Deleting"
	case status == "error":
		p.Status.Phase = "Error"
		p.Status.Message = errMsg
	case status == "done":
		if kind == OpDelete {
			// Project will be deleted from KV by final worker.
		} else {
			p.Status.Phase = "Ready"
			p.Status.Message = "ready"
		}
	}
	p.Status.UpdatedAt = time.Now().UTC()
	p.Status.LastOpID = opID
	p.Status.LastOpKind = string(kind)
	_ = store.PutProject(ctx, p)
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// HTTP API + UI
////////////////////////////////////////////////////////////////////////////////

type API struct {
	nc        *nats.Conn
	store     *Store
	artifacts ArtifactStore
	waiters   *waiterHub
}

func (a *API) routes() http.Handler {
	mux := http.NewServeMux()

	// Static UI
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// CRUD: projects
	mux.HandleFunc("/api/projects", a.handleProjects)
	mux.HandleFunc("/api/projects/", a.handleProjectByID)
	mux.HandleFunc("/api/events/registration", a.handleRegistrationEvents)
	mux.HandleFunc("/api/webhooks/source", a.handleSourceRepoWebhook)

	// Ops: read
	mux.HandleFunc("/api/ops/", a.handleOpByID)

	return a.withRequestLogging(mux)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(p)
}

func (a *API) withRequestLogging(next http.Handler) http.Handler {
	apiLog := appLog.Source("api")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		dur := time.Since(started).Round(time.Millisecond)
		msg := fmt.Sprintf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, dur)
		switch {
		case rec.status >= 500:
			apiLog.Errorf("%s", msg)
		case rec.status >= 400:
			apiLog.Warnf("%s", msg)
		default:
			apiLog.Infof("%s", msg)
		}
	})
}

type RegistrationEvent struct {
	Action    string      `json:"action"` // create|update|delete
	ProjectID string      `json:"project_id,omitempty"`
	Spec      ProjectSpec `json:"spec,omitempty"`
}

type SourceRepoWebhookEvent struct {
	ProjectID string `json:"project_id"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Ref       string `json:"ref,omitempty"` // e.g. refs/heads/main
	Commit    string `json:"commit,omitempty"`
}

func (a *API) createProjectFromSpec(ctx context.Context, spec ProjectSpec) (Project, Operation, WorkerResultMsg, error) {
	spec = normalizeProjectSpec(spec)
	if err := validateProjectSpec(spec); err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}

	projectID := newID()
	now := time.Now().UTC()
	p := Project{
		ID:        projectID,
		CreatedAt: now,
		UpdatedAt: now,
		Spec:      spec,
		Status: ProjectStatus{
			Phase:      "Reconciling",
			UpdatedAt:  now,
			LastOpID:   "",
			LastOpKind: "",
			Message:    "queued",
		},
	}
	if err := a.store.PutProject(ctx, p); err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, fmt.Errorf("failed to persist project")
	}

	op, final, err := a.runOp(ctx, OpCreate, projectID, spec)
	if err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}
	p, _ = a.store.GetProject(ctx, projectID)
	return p, op, final, nil
}

func (a *API) updateProjectFromSpec(ctx context.Context, projectID string, spec ProjectSpec) (Project, Operation, WorkerResultMsg, error) {
	spec = normalizeProjectSpec(spec)
	if err := validateProjectSpec(spec); err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}

	p, err := a.store.GetProject(ctx, projectID)
	if err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}
	p.Spec = spec
	p.Status.Phase = "Reconciling"
	p.Status.Message = "queued update"
	p.Status.UpdatedAt = time.Now().UTC()
	if err := a.store.PutProject(ctx, p); err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, fmt.Errorf("failed to persist project")
	}

	op, final, err := a.runOp(ctx, OpUpdate, projectID, spec)
	if err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}
	p, _ = a.store.GetProject(ctx, projectID)
	return p, op, final, nil
}

func (a *API) deleteProject(ctx context.Context, projectID string) (Operation, WorkerResultMsg, error) {
	p, err := a.store.GetProject(ctx, projectID)
	if err != nil {
		return Operation{}, WorkerResultMsg{}, err
	}
	p.Status.Phase = "Deleting"
	p.Status.Message = "queued delete"
	p.Status.UpdatedAt = time.Now().UTC()
	_ = a.store.PutProject(ctx, p)

	op, final, err := a.runOp(ctx, OpDelete, projectID, ProjectSpec{})
	if err != nil {
		return Operation{}, WorkerResultMsg{}, err
	}
	return op, final, nil
}

func (a *API) handleRegistrationEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var evt RegistrationEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	evt.Action = strings.TrimSpace(strings.ToLower(evt.Action))

	switch evt.Action {
	case "create":
		project, op, final, err := a.createProjectFromSpec(r.Context(), evt.Spec)
		if err != nil {
			if strings.Contains(err.Error(), "must") || strings.Contains(err.Error(), "invalid") {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"project": project,
			"op":      op,
			"final":   final,
		})
	case "update":
		projectID := strings.TrimSpace(evt.ProjectID)
		if projectID == "" {
			http.Error(w, "project_id required", http.StatusBadRequest)
			return
		}
		project, op, final, err := a.updateProjectFromSpec(r.Context(), projectID, evt.Spec)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if strings.Contains(err.Error(), "must") || strings.Contains(err.Error(), "invalid") {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"project": project,
			"op":      op,
			"final":   final,
		})
	case "delete":
		projectID := strings.TrimSpace(evt.ProjectID)
		if projectID == "" {
			http.Error(w, "project_id required", http.StatusBadRequest)
			return
		}
		op, final, err := a.deleteProject(r.Context(), projectID)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"deleted": true,
			"op":      op,
			"final":   final,
		})
	default:
		http.Error(w, "action must be create, update, or delete", http.StatusBadRequest)
	}
}

func normalizeBranchValue(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "refs/heads/")
	v = strings.TrimPrefix(v, "heads/")
	return v
}

func isMainBranchWebhook(branch, ref string) bool {
	// Support either plain branch names ("main") or refs ("refs/heads/main")
	// from webhook providers and accept either field if present.
	return normalizeBranchValue(branch) == "main" || normalizeBranchValue(ref) == "main"
}

func (a *API) handleSourceRepoWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var evt SourceRepoWebhookEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	evt.ProjectID = strings.TrimSpace(evt.ProjectID)
	if evt.ProjectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	if evt.Repo != "" && strings.ToLower(strings.TrimSpace(evt.Repo)) != "source" {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": false,
			"reason":   "ignored: only source repo webhooks trigger ci",
		})
		return
	}
	if !isMainBranchWebhook(evt.Branch, evt.Ref) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": false,
			"reason":   "ignored: only main branch triggers CI",
		})
		return
	}

	p, err := a.store.GetProject(r.Context(), evt.ProjectID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read project", http.StatusInternalServerError)
		return
	}

	op, _, err := a.runOp(r.Context(), OpCI, p.ID, p.Spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"trigger":  "source.main.webhook",
		"project":  p.ID,
		"op":       op,
		"commit":   evt.Commit,
	})
}

func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projects, err := a.store.ListProjects(r.Context())
		if err != nil {
			http.Error(w, "failed to list projects", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, projects)

	case http.MethodPost:
		var spec ProjectSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		spec = normalizeProjectSpec(spec)
		if err := validateProjectSpec(spec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		projectID := newID()
		now := time.Now().UTC()

		p := Project{
			ID:        projectID,
			CreatedAt: now,
			UpdatedAt: now,
			Spec:      spec,
			Status: ProjectStatus{
				Phase:      "Reconciling",
				UpdatedAt:  now,
				LastOpID:   "",
				LastOpKind: "",
				Message:    "queued",
			},
		}
		if err := a.store.PutProject(r.Context(), p); err != nil {
			http.Error(w, "failed to persist project", http.StatusInternalServerError)
			return
		}

		op, final, err := a.runOp(r.Context(), OpCreate, projectID, spec)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Return project + last op for the UI
		p, _ = a.store.GetProject(r.Context(), projectID)
		writeJSON(w, http.StatusOK, map[string]any{
			"project": p,
			"op":      op,
			"final":   final,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleProjectByID(w http.ResponseWriter, r *http.Request) {
	// /api/projects/{id}
	if !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		http.NotFound(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 1 {
		// Route /api/projects/{id}/artifacts...
		if parts[1] == "artifacts" {
			a.handleProjectArtifacts(w, r)
			return
		}
		// Reject unknown subresources under /api/projects/{id}/...
		http.NotFound(w, r)
		return
	}

	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		p, err := a.store.GetProject(r.Context(), projectID)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "failed to read project", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, p)

	case http.MethodPut:
		var spec ProjectSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		spec = normalizeProjectSpec(spec)
		if err := validateProjectSpec(spec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		p, err := a.store.GetProject(r.Context(), projectID)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "failed to read project", http.StatusInternalServerError)
			return
		}

		p.Spec = spec
		p.Status.Phase = "Reconciling"
		p.Status.Message = "queued update"
		p.Status.UpdatedAt = time.Now().UTC()
		if err := a.store.PutProject(r.Context(), p); err != nil {
			http.Error(w, "failed to persist project", http.StatusInternalServerError)
			return
		}

		op, final, err := a.runOp(r.Context(), OpUpdate, projectID, spec)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		p, _ = a.store.GetProject(r.Context(), projectID)
		writeJSON(w, http.StatusOK, map[string]any{
			"project": p,
			"op":      op,
			"final":   final,
		})

	case http.MethodDelete:
		// Mark deleting early
		p, err := a.store.GetProject(r.Context(), projectID)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "failed to read project", http.StatusInternalServerError)
			return
		}
		p.Status.Phase = "Deleting"
		p.Status.Message = "queued delete"
		p.Status.UpdatedAt = time.Now().UTC()
		_ = a.store.PutProject(r.Context(), p)

		op, final, err := a.runOp(r.Context(), OpDelete, projectID, ProjectSpec{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"deleted": true,
			"op":      op,
			"final":   final,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleProjectArtifacts(w http.ResponseWriter, r *http.Request) {
	// Routes:
	//  - GET /api/projects/{id}/artifacts              -> list files
	//  - GET /api/projects/{id}/artifacts/{path...}    -> download file
	if !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		http.NotFound(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "artifacts" {
		http.NotFound(w, r)
		return
	}

	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// list
	if len(parts) == 2 {
		files, err := a.artifacts.ListFiles(projectID)
		if err != nil {
			http.Error(w, "failed to list artifacts", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": files})
		return
	}

	// download
	relPath := strings.Join(parts[2:], "/")
	relPath = strings.TrimPrefix(relPath, "/")
	data, err := a.artifacts.ReadFile(projectID, relPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read artifact", http.StatusInternalServerError)
		return
	}

	// Minimal content type handling
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(relPath)))
	_, _ = w.Write(data)
}

func (a *API) handleOpByID(w http.ResponseWriter, r *http.Request) {
	// GET /api/ops/{id}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	opID := strings.TrimPrefix(r.URL.Path, "/api/ops/")
	opID = strings.TrimSpace(opID)
	if opID == "" || strings.Contains(opID, "/") {
		http.Error(w, "bad op id", http.StatusBadRequest)
		return
	}
	op, err := a.store.GetOp(r.Context(), opID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read op", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (a *API) runOp(ctx context.Context, kind OperationKind, projectID string, spec ProjectSpec) (Operation, WorkerResultMsg, error) {
	apiLog := appLog.Source("api")
	opID := newID()
	now := time.Now().UTC()

	// Persist initial op record
	op := Operation{
		ID:        opID,
		Kind:      kind,
		ProjectID: projectID,
		Requested: now,
		Status:    "queued",
		Steps:     []OpStep{},
	}
	if err := a.store.PutOp(ctx, op); err != nil {
		return Operation{}, WorkerResultMsg{}, fmt.Errorf("persist op: %w", err)
	}
	apiLog.Infof("queued op=%s kind=%s project=%s", opID, kind, projectID)

	// Update project status (except delete might be removed later)
	if kind != OpDelete {
		p, err := a.store.GetProject(ctx, projectID)
		if err == nil {
			p.Spec = spec
			msg := "queued"
			if kind == OpCI {
				msg = "queued ci from source webhook"
			}
			p.Status = ProjectStatus{
				Phase:      "Reconciling",
				UpdatedAt:  now,
				LastOpID:   opID,
				LastOpKind: string(kind),
				Message:    msg,
			}
			_ = a.store.PutProject(ctx, p)
		}
	} else {
		_ = finalizeOp(ctx, a.store, opID, projectID, kind, "running", "")
	}

	// Register waiter before publish
	ch := a.waiters.register(opID)
	defer a.waiters.unregister(opID)

	// Publish start message
	msg := ProjectOpMsg{
		OpID:      opID,
		Kind:      kind,
		ProjectID: projectID,
		Spec:      spec,
		At:        now,
	}
	b, _ := json.Marshal(msg)
	startSubject := subjectProjectOpStart
	if kind == OpCI {
		startSubject = subjectBootstrapDone
	}
	if err := a.nc.Publish(startSubject, b); err != nil {
		_ = finalizeOp(context.Background(), a.store, opID, projectID, kind, "error", err.Error())
		apiLog.Errorf("publish failed op=%s kind=%s project=%s: %v", opID, kind, projectID, err)
		return Operation{}, WorkerResultMsg{}, fmt.Errorf("publish op: %w", err)
	}
	apiLog.Debugf("published op=%s subject=%s", opID, startSubject)

	// Wait for final worker completion
	waitCtx, cancel := context.WithTimeout(ctx, apiWaitTimeout)
	defer cancel()

	var final WorkerResultMsg
	select {
	case <-waitCtx.Done():
		_ = finalizeOp(context.Background(), a.store, opID, projectID, kind, "error", "timeout waiting for workers")
		apiLog.Errorf("timeout op=%s kind=%s project=%s", opID, kind, projectID)
		return Operation{}, WorkerResultMsg{}, fmt.Errorf("timeout waiting for workers")
	case final = <-ch:
	}

	if final.Err != "" {
		_ = finalizeOp(context.Background(), a.store, opID, projectID, kind, "error", final.Err)
		apiLog.Errorf("op=%s failed in %s: %s", opID, final.Worker, final.Err)
		return Operation{}, final, errors.New(final.Err)
	}

	// Fetch final op state for response
	op, _ = a.store.GetOp(ctx, opID)
	apiLog.Infof("completed op=%s kind=%s project=%s", opID, kind, projectID)
	return op, final, nil
}

////////////////////////////////////////////////////////////////////////////////
// NATS subscription for final deploy worker completion to wake API waiters
////////////////////////////////////////////////////////////////////////////////

func subscribeFinalResults(nc *nats.Conn, waiters *waiterHub) (*nats.Subscription, error) {
	sub, err := nc.Subscribe(subjectDeployDone, func(m *nats.Msg) {
		var msg WorkerResultMsg
		if err := json.Unmarshal(m.Data, &msg); err != nil {
			return
		}
		waiters.deliver(msg.OpID, msg)
	})
	if err != nil {
		return nil, err
	}
	return sub, nil
}

////////////////////////////////////////////////////////////////////////////////
// Utilities
////////////////////////////////////////////////////////////////////////////////

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func mustJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

////////////////////////////////////////////////////////////////////////////////
// main
////////////////////////////////////////////////////////////////////////////////

func main() {
	mainLog := appLog.Source("main")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1) embedded NATS
	ns, natsURL, jsDir, err := startEmbeddedNATS()
	if err != nil {
		mainLog.Fatalf("start embedded nats: %v", err)
	}
	defer func() {
		ns.Shutdown()
		ns.WaitForShutdown()
		_ = os.RemoveAll(jsDir)
	}()

	// 2) NATS client for API/orchestration
	nc, err := nats.Connect(natsURL, nats.Name("api"))
	if err != nil {
		mainLog.Fatalf("connect nats: %v", err)
	}
	defer nc.Drain()

	js, err := jetstream.New(nc)
	if err != nil {
		mainLog.Fatalf("jetstream: %v", err)
	}

	kv := &KV{js: js}
	store, err := newStore(ctx, kv)
	if err != nil {
		mainLog.Fatalf("store: %v", err)
	}

	// 3) Artifacts root
	artifacts := NewFSArtifacts(defaultArtifactsRoot)
	if err := os.MkdirAll(defaultArtifactsRoot, 0o755); err != nil {
		mainLog.Fatalf("mkdir artifacts root: %v", err)
	}

	// 4) Start worker pipeline
	workers := []Worker{
		NewRegistrationWorker(natsURL, artifacts),
		NewRepoBootstrapWorker(natsURL, artifacts),
		NewImageBuilderWorker(natsURL, artifacts),
		NewManifestRendererWorker(natsURL, artifacts),
	}
	for _, w := range workers {
		if err := w.Start(ctx); err != nil {
			mainLog.Fatalf("start worker: %v", err)
		}
	}

	// 5) Subscribe to final results and multiplex to HTTP requests
	waiters := newWaiterHub()
	finalSub, err := subscribeFinalResults(nc, waiters)
	if err != nil {
		mainLog.Fatalf("subscribe final: %v", err)
	}
	defer finalSub.Unsubscribe()

	if err := nc.Flush(); err != nil {
		mainLog.Fatalf("flush: %v", err)
	}

	// 6) HTTP server
	api := &API{
		nc:        nc,
		store:     store,
		artifacts: artifacts,
		waiters:   waiters,
	}
	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           api.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	mainLog.Infof("NATS: %s", natsURL)
	mainLog.Infof("Portal: http://%s", httpAddr)
	mainLog.Infof("Artifacts root: %s", defaultArtifactsRoot)
	mainLog.Infof("Try: create/update/delete projects; delete cleans project artifacts dir")

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		mainLog.Fatalf("http server: %v", err)
	}
}

////////////////////////////////////////////////////////////////////////////////
// (Optional) tiny helper endpoint dev tooling could use (not wired into mux above)
////////////////////////////////////////////////////////////////////////////////

func copyStream(dst io.Writer, src io.Reader) error {
	_, err := io.Copy(dst, src)
	return err
}
