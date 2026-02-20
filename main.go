// File: main.go
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	// API publishes project operations here.
	subjectProjectOpStart = "paas.project.op.start"

	// Worker pipeline chain.
	subjectRegistrationDone = "paas.project.op.registration.done"
	subjectBootstrapDone    = "paas.project.op.bootstrap.done"
	subjectBuildDone        = "paas.project.op.build.done"
	subjectDeployDone       = "paas.project.op.deploy.done"

	// KV buckets.
	kvBucketProjects = "paas_projects"
	kvBucketOps      = "paas_ops"

	// Project keys in KV.
	kvProjectKeyPrefix = "project/"
	kvOpKeyPrefix      = "op/"

	// HTTP.
	httpAddr = "127.0.0.1:8080"

	// Where workers write artifacts.
	defaultArtifactsRoot = "./data/artifacts"

	// API wait timeout per request.
	apiWaitTimeout = 45 * time.Second

	// Schema defaults (from cfg/project-jsonschema.json).
	projectAPIVersion = "platform.example.com/v2"
	projectKind       = "App"

	defaultKVProjectHistory = 25
	defaultKVOpsHistory     = 50
	defaultStartupWait      = 10 * time.Second
	defaultReadHeaderWait   = 5 * time.Second
	gitOpTimeout            = 20 * time.Second
	gitReadTimeout          = 10 * time.Second
	maxEnvVarValueLength    = 4096
	shortIDLength           = 12
	httpServerErrThreshold  = 500
	httpClientErrThreshold  = 400

	fileModePrivate        os.FileMode = 0o600
	fileModeExecPrivate    os.FileMode = 0o700
	dirModePrivateRead     os.FileMode = 0o750
	projectRelPathPartsMin             = 2
	touchedArtifactsCap                = 8

	networkPolicyInternal = "internal"
	branchMain            = "main"
	projectPhaseReady     = "Ready"
	projectPhaseError     = "Error"
	projectPhaseDel       = "Deleting"
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
	Finished  time.Time     `json:"finished"`
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
		spec.NetworkPolicies.Ingress = networkPolicyInternal
	}
	if spec.NetworkPolicies.Egress == "" {
		spec.NetworkPolicies.Egress = networkPolicyInternal
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
	if err := validateProjectCore(spec); err != nil {
		return err
	}
	if err := validateCapabilities(spec.Capabilities); err != nil {
		return err
	}
	if err := validateEnvironments(spec.Environments); err != nil {
		return err
	}
	return validateNetworkPolicies(spec.NetworkPolicies)
}

func validateProjectCore(spec ProjectSpec) error {
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
	return nil
}

func validateCapabilities(capabilities []string) error {
	for _, capability := range capabilities {
		if len(capability) > 64 || !capabilityRe.MatchString(capability) {
			return fmt.Errorf("invalid capability %q", capability)
		}
	}
	return nil
}

func validateEnvironments(envs map[string]EnvConfig) error {
	if len(envs) < 1 {
		return errors.New("environments must include at least one environment")
	}
	for envName, envCfg := range envs {
		if len(envName) > 32 || !envNameRe.MatchString(envName) {
			return fmt.Errorf("invalid environment name %q", envName)
		}
		if err := validateEnvironmentVars(envName, envCfg.Vars); err != nil {
			return err
		}
	}
	return nil
}

func validateEnvironmentVars(envName string, vars map[string]string) error {
	for key, value := range vars {
		if len(key) > 128 || !envVarNameRe.MatchString(key) {
			return fmt.Errorf("invalid environment variable name %q in %q", key, envName)
		}
		if len(value) > maxEnvVarValueLength {
			return fmt.Errorf("env var %q in %q exceeds max length", key, envName)
		}
	}
	return nil
}

func validateNetworkPolicies(policies NetworkPolicies) error {
	if !networkValueRe.MatchString(policies.Ingress) {
		return errors.New("networkPolicies.ingress must be internal or none")
	}
	if !networkValueRe.MatchString(policies.Egress) {
		return errors.New("networkPolicies.egress must be internal or none")
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
	Spec      ProjectSpec   `json:"spec"` // create/update only
	Err       string        `json:"err,omitempty"`
	At        time.Time     `json:"at"`
}

type WorkerResultMsg struct {
	OpID      string        `json:"op_id"`
	Kind      OperationKind `json:"kind"`
	ProjectID string        `json:"project_id"`
	Spec      ProjectSpec   `json:"spec"`
	Worker    string        `json:"worker"`
	Message   string        `json:"message,omitempty"`
	Err       string        `json:"err,omitempty"`
	Artifacts []string      `json:"artifacts,omitempty"` // relative paths
	At        time.Time     `json:"at"`
}

func zeroProjectSpec() ProjectSpec {
	return ProjectSpec{
		APIVersion:      "",
		Kind:            "",
		Name:            "",
		Runtime:         "",
		Capabilities:    nil,
		Environments:    nil,
		NetworkPolicies: NetworkPolicies{Ingress: "", Egress: ""},
	}
}

func newWorkerResultMsg(message string) WorkerResultMsg {
	return WorkerResultMsg{
		OpID:      "",
		Kind:      "",
		ProjectID: "",
		Spec:      zeroProjectSpec(),
		Worker:    "",
		Message:   message,
		Err:       "",
		Artifacts: nil,
		At:        time.Time{},
	}
}

////////////////////////////////////////////////////////////////////////////////
// Infrastructure: Embedded NATS + JetStream KV
////////////////////////////////////////////////////////////////////////////////

func ensureKVBucket(
	ctx context.Context,
	js jetstream.JetStream,
	bucket string,
	history uint8,
	out *jetstream.KeyValue,
) error {
	var cfg jetstream.KeyValueConfig
	cfg.Bucket = bucket
	cfg.History = history

	createdKV, err := js.CreateKeyValue(ctx, cfg)
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketExists) {
			existingKV, getErr := js.KeyValue(ctx, bucket)
			if getErr != nil {
				return getErr
			}
			*out = existingKV
			return nil
		}
		return err
	}
	*out = createdKV
	return nil
}

func startEmbeddedNATS() (*server.Server, string, string, error) {
	storeDir, err := os.MkdirTemp("", "nats-js-*")
	if err != nil {
		return nil, "", "", err
	}
	var opts server.Options
	opts.ServerName = "embedded-paas"
	opts.Host = "127.0.0.1"
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = storeDir
	opts.NoSigs = true

	ns, err := server.NewServer(&opts)
	if err != nil {
		_ = os.RemoveAll(storeDir)
		return nil, "", "", err
	}
	ns.ConfigureLogger()
	ns.Start()
	if !ns.ReadyForConnections(defaultStartupWait) {
		ns.Shutdown()
		ns.WaitForShutdown()
		_ = os.RemoveAll(storeDir)
		return nil, "", "", errors.New("nats not ready")
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

func newStore(ctx context.Context, js jetstream.JetStream) (*Store, error) {
	var projectsKV jetstream.KeyValue
	err := ensureKVBucket(ctx, js, kvBucketProjects, defaultKVProjectHistory, &projectsKV)
	if err != nil {
		return nil, err
	}
	var opsKV jetstream.KeyValue
	err = ensureKVBucket(ctx, js, kvBucketOps, defaultKVOpsHistory, &opsKV)
	if err != nil {
		return nil, err
	}
	return &Store{kvProjects: projectsKV, kvOps: opsKV}, nil
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
	unmarshalErr := json.Unmarshal(e.Value(), &p)
	if unmarshalErr != nil {
		return Project{}, unmarshalErr
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
		project, getErr := s.GetProject(ctx, projectID)
		if getErr != nil {
			// best-effort listing
			continue
		}
		out = append(out, project)
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
	unmarshalErr := json.Unmarshal(e.Value(), &op)
	if unmarshalErr != nil {
		return Operation{}, unmarshalErr
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
	if err := os.MkdirAll(dir, dirModePrivateRead); err != nil {
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
		return "", errors.New("invalid relPath")
	}
	full := filepath.Join(dir, relPath)
	mkdirErr := os.MkdirAll(filepath.Dir(full), dirModePrivateRead)
	if mkdirErr != nil {
		return "", mkdirErr
	}
	writeErr := os.WriteFile(full, data, fileModePrivate)
	if writeErr != nil {
		return "", writeErr
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
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, _ error) error {
		if d == nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
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
		return nil, errors.New("invalid relPath")
	}
	full := filepath.Join(dir, relPath)
	if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(dir)+string(filepath.Separator)) {
		return nil, errors.New("invalid relPath")
	}
	//nolint:gosec // full path is verified to stay inside the project directory.
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
	return &waiterHub{
		mu:      sync.Mutex{},
		waiters: map[string]chan WorkerResultMsg{},
	}
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

////////////////////////////////////////////////////////////////////////////////
// Worker actions: real-world-ish PaaS artifacts
////////////////////////////////////////////////////////////////////////////////

func registrationWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("registration worker starting")
	_ = markOpStepStart(ctx, store, msg.OpID, "registrar", stepStart, "register app configuration")

	spec := normalizeProjectSpec(msg.Spec)
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate:
		outcome, err = runRegistrationCreateOrUpdate(artifacts, msg, spec)
	case OpDelete:
		outcome, err = runRegistrationDelete(artifacts, msg.ProjectID, msg.OpID)
	case OpCI:
		outcome = repoBootstrapOutcome{
			message:   "registration skipped for ci operation",
			artifacts: nil,
		}
	default:
		err = fmt.Errorf("unknown op kind: %s", msg.Kind)
	}
	if err != nil {
		_ = markOpStepEnd(
			ctx,
			store,
			msg.OpID,
			"registrar",
			time.Now().UTC(),
			"",
			err.Error(),
			outcome.artifacts,
		)
		return res, err
	}

	res.Message = outcome.message
	res.Artifacts = outcome.artifacts
	_ = markOpStepEnd(
		ctx,
		store,
		msg.OpID,
		"registrar",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runRegistrationCreateOrUpdate(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
) (repoBootstrapOutcome, error) {
	if err := validateProjectSpec(spec); err != nil {
		return newRepoBootstrapOutcome(), err
	}
	_, _ = artifacts.EnsureProjectDir(msg.ProjectID)
	projectYAMLPath, err := artifacts.WriteFile(
		msg.ProjectID,
		"registration/project.yaml",
		renderProjectConfigYAML(spec),
	)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	registrationPath, err := artifacts.WriteFile(
		msg.ProjectID,
		"registration/registration.json",
		mustJSON(map[string]any{
			"project_id": msg.ProjectID,
			"op_id":      msg.OpID,
			"kind":       msg.Kind,
			"registered": time.Now().UTC(),
			"name":       spec.Name,
			"runtime":    spec.Runtime,
		}),
	)
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: []string{projectYAMLPath},
		}, err
	}
	return repoBootstrapOutcome{
		message:   "project registration upserted",
		artifacts: []string{projectYAMLPath, registrationPath},
	}, nil
}

func runRegistrationDelete(
	artifacts ArtifactStore,
	projectID, opID string,
) (repoBootstrapOutcome, error) {
	deregisterBody := fmt.Appendf(
		nil,
		"deregister requested at %s\nop=%s\n",
		time.Now().UTC().Format(time.RFC3339),
		opID,
	)
	deregisterPath, err := artifacts.WriteFile(
		projectID,
		"registration/deregister.txt",
		deregisterBody,
	)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	return repoBootstrapOutcome{
		message:   "project deregistration staged",
		artifacts: []string{deregisterPath},
	}, nil
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

func runGitCmd(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitHasStagedChanges(ctx context.Context, dir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet", "--exit-code")
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

func gitCommitIfChanged(ctx context.Context, dir, message string) (bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	if err := runGitCmd(runCtx, dir, "add", "-A"); err != nil {
		return false, err
	}
	changed, err := gitHasStagedChanges(runCtx, dir)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	commitErr := runGitCmd(runCtx, dir, "commit", "-m", message)
	if commitErr != nil {
		return false, commitErr
	}
	return true, nil
}

func gitRevParse(ctx context.Context, dir, ref string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", "rev-parse", ref)
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

func ensureLocalGitRepo(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, dirModePrivateRead); err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		initErr := runGitCmd(runCtx, dir, "init", "-b", branchMain)
		if initErr != nil {
			// Fallback for older git versions that do not support `-b`.
			fallbackErr := runGitCmd(runCtx, dir, "init")
			if fallbackErr != nil {
				return fmt.Errorf("git init failed: %w; fallback failed: %w", initErr, fallbackErr)
			}
		}
	}
	if err := runGitCmd(runCtx, dir, "checkout", "-B", branchMain); err != nil {
		return err
	}
	if err := runGitCmd(runCtx, dir, "config", "user.name", "Local PaaS Bot"); err != nil {
		return err
	}
	if err := runGitCmd(runCtx, dir, "config", "user.email", "paas-local@example.invalid"); err != nil {
		return err
	}
	if err := runGitCmd(runCtx, dir, "config", "commit.gpgsign", "false"); err != nil {
		return err
	}
	return nil
}

func writeFileIfMissing(path string, data []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirModePrivateRead); err != nil {
		return false, err
	}
	writeErr := os.WriteFile(path, data, fileModePrivate)
	if writeErr != nil {
		return false, writeErr
	}
	return true, nil
}

func upsertFile(path string, data []byte) (bool, error) {
	prev, err := os.ReadFile(path)
	if err == nil && string(prev) == string(data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	mkdirErr := os.MkdirAll(filepath.Dir(path), dirModePrivateRead)
	if mkdirErr != nil {
		return false, mkdirErr
	}
	writeErr := os.WriteFile(path, data, fileModePrivate)
	if writeErr != nil {
		return false, writeErr
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
if [ "$branch" != %q ]; then
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

curl -fsS --max-time %d \
  -H 'Content-Type: application/json' \
  -X POST '%s' \
  -d "{\"project_id\":\"%s\",\"repo\":\"source\",\"branch\":\"${branch}\",\"ref\":\"refs/heads/${branch}\",\"commit\":\"${commit}\"}" \
  >/dev/null || true
`, branchMain, projectRelPathPartsMin, endpoint, projectID)
}

func installSourceWebhookHooks(repoDir, projectID, endpoint string) error {
	script := []byte(renderSourceWebhookHookScript(projectID, endpoint))
	for _, hook := range []string{"post-commit", "post-merge"} {
		hookPath := filepath.Join(repoDir, ".git", "hooks", hook)
		if err := os.MkdirAll(filepath.Dir(hookPath), dirModePrivateRead); err != nil {
			return err
		}
		if err := os.WriteFile(hookPath, script, fileModeExecPrivate); err != nil {
			return err
		}
		if err := os.Chmod(hookPath, fileModeExecPrivate); err != nil {
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
	slices.Sort(out)
	return out
}

type repoBootstrapOutcome struct {
	message   string
	artifacts []string
}

func newRepoBootstrapOutcome() repoBootstrapOutcome {
	return repoBootstrapOutcome{
		message:   "",
		artifacts: nil,
	}
}

func repoBootstrapWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("repo bootstrap worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"repoBootstrap",
		stepStart,
		"bootstrap source and manifests repos",
	)

	spec := normalizeProjectSpec(msg.Spec)
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate:
		outcome, err = runRepoBootstrapCreateOrUpdate(ctx, artifacts, msg, spec)
	case OpDelete:
		outcome, err = runRepoBootstrapDelete(artifacts, msg.ProjectID)
	case OpCI:
		outcome = repoBootstrapOutcome{
			message:   "repo bootstrap skipped for ci operation",
			artifacts: nil,
		}
	default:
		err = fmt.Errorf("unknown op kind: %s", msg.Kind)
	}
	if err != nil {
		_ = markOpStepEnd(
			ctx,
			store,
			msg.OpID,
			"repoBootstrap",
			time.Now().UTC(),
			"",
			err.Error(),
			outcome.artifacts,
		)
		return res, err
	}

	res.Message = outcome.message
	res.Artifacts = outcome.artifacts
	_ = markOpStepEnd(
		ctx,
		store,
		msg.OpID,
		"repoBootstrap",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runRepoBootstrapDelete(
	artifacts ArtifactStore,
	projectID string,
) (repoBootstrapOutcome, error) {
	planPath, err := artifacts.WriteFile(
		projectID,
		"repos/teardown-plan.txt",
		[]byte("archive source repo\narchive manifests repo\nremove project workspace\n"),
	)
	if err != nil {
		return repoBootstrapOutcome{}, err
	}
	return repoBootstrapOutcome{
		message:   "repository teardown plan generated",
		artifacts: []string{planPath},
	}, nil
}

func runRepoBootstrapCreateOrUpdate(
	ctx context.Context,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
) (repoBootstrapOutcome, error) {
	projectDir, sourceDir, manifestsDir, err := ensureBootstrapRepos(ctx, artifacts, msg.ProjectID)
	if err != nil {
		return repoBootstrapOutcome{}, err
	}

	touched := make([]string, 0, touchedArtifactsCap)
	err = seedSourceRepo(msg, spec, projectDir, sourceDir, &touched)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}
	err = seedManifestsRepo(msg, spec, projectDir, manifestsDir, &touched)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}
	err = commitBootstrapSeeds(ctx, msg, sourceDir, manifestsDir)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}

	webhookURL, err := configureSourceWebhook(ctx, msg, projectDir, sourceDir, &touched)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}
	err = writeBootstrapSummary(ctx, msg, projectDir, sourceDir, manifestsDir, webhookURL, &touched)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}
	return repoBootstrapOutcome{
		message:   "bootstrapped local source/manifests git repos and installed source webhook",
		artifacts: uniqueSorted(touched),
	}, nil
}

func ensureBootstrapRepos(
	ctx context.Context,
	artifacts ArtifactStore,
	projectID string,
) (string, string, string, error) {
	projectDir, err := artifacts.EnsureProjectDir(projectID)
	if err != nil {
		return "", "", "", err
	}
	sourceDir := sourceRepoDir(artifacts, projectID)
	manifestsDir := manifestsRepoDir(artifacts, projectID)
	sourceRepoErr := ensureLocalGitRepo(ctx, sourceDir)
	if sourceRepoErr != nil {
		return "", "", "", sourceRepoErr
	}
	manifestsRepoErr := ensureLocalGitRepo(ctx, manifestsDir)
	if manifestsRepoErr != nil {
		return "", "", "", manifestsRepoErr
	}
	return projectDir, sourceDir, manifestsDir, nil
}

func seedSourceRepo(
	msg ProjectOpMsg,
	spec ProjectSpec,
	projectDir, sourceDir string,
	touched *[]string,
) error {
	sourceReadme := filepath.Join(sourceDir, "README.md")
	sourceReadmeBody := fmt.Appendf(nil, "# %s source\n\nRuntime: %s\n", spec.Name, spec.Runtime)
	readmeCreated, err := writeFileIfMissing(
		sourceReadme,
		sourceReadmeBody,
	)
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, sourceReadme, readmeCreated)

	sourceMain := filepath.Join(sourceDir, "main.go")
	sourceMainBody := fmt.Appendf(nil, `package main

import "fmt"

func main() { fmt.Println("hello from %s") }
`, spec.Name)
	mainCreated, err := writeFileIfMissing(sourceMain, sourceMainBody)
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, sourceMain, mainCreated)

	sourceRepoMeta := filepath.Join(sourceDir, ".paas", "repo.json")
	metaUpdated, err := upsertFile(sourceRepoMeta, mustJSON(map[string]any{
		"project_id": msg.ProjectID,
		"repo":       "source",
		"path":       sourceDir,
		"branch":     branchMain,
	}))
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, sourceRepoMeta, metaUpdated)
	return nil
}

func seedManifestsRepo(
	msg ProjectOpMsg,
	spec ProjectSpec,
	projectDir, manifestsDir string,
	touched *[]string,
) error {
	manifestsReadme := filepath.Join(manifestsDir, "README.md")
	manifestsReadmeBody := fmt.Appendf(
		nil,
		"# %s manifests\n\nTarget image: local/%s:latest\n",
		spec.Name,
		safeName(spec.Name),
	)
	readmeCreated, err := writeFileIfMissing(
		manifestsReadme,
		manifestsReadmeBody,
	)
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, manifestsReadme, readmeCreated)

	manifestsRepoMeta := filepath.Join(manifestsDir, ".paas", "repo.json")
	metaUpdated, err := upsertFile(manifestsRepoMeta, mustJSON(map[string]any{
		"project_id": msg.ProjectID,
		"repo":       "manifests",
		"path":       manifestsDir,
		"branch":     branchMain,
	}))
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, manifestsRepoMeta, metaUpdated)
	return nil
}

func commitBootstrapSeeds(
	ctx context.Context,
	msg ProjectOpMsg,
	sourceDir, manifestsDir string,
) error {
	_, sourceCommitErr := gitCommitIfChanged(
		ctx,
		sourceDir,
		fmt.Sprintf("platform-sync: bootstrap source repo (%s)", shortID(msg.OpID)),
	)
	if sourceCommitErr != nil {
		return sourceCommitErr
	}
	_, manifestsCommitErr := gitCommitIfChanged(
		ctx,
		manifestsDir,
		fmt.Sprintf("platform-sync: bootstrap manifests repo (%s)", shortID(msg.OpID)),
	)
	return manifestsCommitErr
}

func configureSourceWebhook(
	ctx context.Context,
	msg ProjectOpMsg,
	projectDir, sourceDir string,
	touched *[]string,
) (string, error) {
	webhookURL := sourceWebhookEndpoint()
	if err := installSourceWebhookHooks(sourceDir, msg.ProjectID, webhookURL); err != nil {
		return "", err
	}
	webhookMeta := filepath.Join(sourceDir, ".paas", "webhook.json")
	updated, err := upsertFile(webhookMeta, mustJSON(map[string]any{
		"project_id": msg.ProjectID,
		"repo":       "source",
		"branch":     branchMain,
		"endpoint":   webhookURL,
		"hooks":      []string{"post-commit", "post-merge"},
	}))
	if err != nil {
		return "", err
	}
	recordTouched(projectDir, touched, webhookMeta, updated)
	_, commitErr := gitCommitIfChanged(
		ctx,
		sourceDir,
		fmt.Sprintf("platform-sync: configure source webhook (%s)", shortID(msg.OpID)),
	)
	if commitErr != nil {
		return "", commitErr
	}
	return webhookURL, nil
}

func writeBootstrapSummary(
	ctx context.Context,
	msg ProjectOpMsg,
	projectDir, sourceDir, manifestsDir, webhookURL string,
	touched *[]string,
) error {
	sourceHead, _ := gitRevParse(ctx, sourceDir, "HEAD")
	manifestsHead, _ := gitRevParse(ctx, manifestsDir, "HEAD")
	bootstrapInfo := filepath.Join(projectDir, "repos", "bootstrap-local.json")
	updated, err := upsertFile(bootstrapInfo, mustJSON(map[string]any{
		"project_id":         msg.ProjectID,
		"source_repo_path":   sourceDir,
		"source_branch":      branchMain,
		"source_head":        sourceHead,
		"manifests_repo":     manifestsDir,
		"manifests_branch":   branchMain,
		"manifests_head":     manifestsHead,
		"webhook_endpoint":   webhookURL,
		"webhook_event_repo": "source",
	}))
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, bootstrapInfo, updated)
	return nil
}

func recordTouched(projectDir string, touched *[]string, fullPath string, changed bool) {
	if !changed {
		return
	}
	*touched = append(*touched, relPath(projectDir, fullPath))
}

func imageBuilderWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("image builder worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"imageBuilder",
		stepStart,
		"build and publish image to local daemon",
	)

	spec := normalizeProjectSpec(msg.Spec)
	imageTag := fmt.Sprintf("local/%s:%s", safeName(spec.Name), shortID(msg.OpID))
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate, OpCI:
		outcome, err = runImageBuilderBuild(artifacts, msg, spec, imageTag)
	case OpDelete:
		outcome, err = runImageBuilderDelete(artifacts, msg.ProjectID, msg.OpID)
	default:
		err = fmt.Errorf("unknown op kind: %s", msg.Kind)
	}
	if err != nil {
		_ = markOpStepEnd(
			ctx,
			store,
			msg.OpID,
			"imageBuilder",
			time.Now().UTC(),
			"",
			err.Error(),
			outcome.artifacts,
		)
		return res, err
	}

	res.Message = outcome.message
	res.Artifacts = outcome.artifacts
	_ = markOpStepEnd(
		ctx,
		store,
		msg.OpID,
		"imageBuilder",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runImageBuilderBuild(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
) (repoBootstrapOutcome, error) {
	dockerfileBody := fmt.Appendf(nil, `FROM alpine:3.20
WORKDIR /app
COPY . .
CMD ["sh", "-c", "echo running %s (%s) && sleep infinity"]
`, spec.Name, spec.Runtime)
	dockerfilePath, err := artifacts.WriteFile(msg.ProjectID, "build/Dockerfile", dockerfileBody)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	publishPath, err := artifacts.WriteFile(
		msg.ProjectID,
		"build/publish-local-daemon.json",
		mustJSON(map[string]any{
			"op_id":         msg.OpID,
			"project_id":    msg.ProjectID,
			"image":         imageTag,
			"runtime":       spec.Runtime,
			"published_at":  time.Now().UTC().Format(time.RFC3339),
			"daemon_target": "local",
		}),
	)
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: []string{dockerfilePath},
		}, err
	}
	imagePath, err := artifacts.WriteFile(msg.ProjectID, "build/image.txt", []byte(imageTag+"\n"))
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: []string{dockerfilePath, publishPath},
		}, err
	}
	return repoBootstrapOutcome{
		message:   "container image built and published to local daemon",
		artifacts: []string{dockerfilePath, publishPath, imagePath},
	}, nil
}

func runImageBuilderDelete(
	artifacts ArtifactStore,
	projectID, opID string,
) (repoBootstrapOutcome, error) {
	pruneBody := fmt.Appendf(nil, "prune local image for project=%s op=%s\n", projectID, opID)
	prunePath, err := artifacts.WriteFile(projectID, "build/image-prune.txt", pruneBody)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	return repoBootstrapOutcome{
		message:   "container prune plan generated",
		artifacts: []string{prunePath},
	}, nil
}

func manifestRendererWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("manifest renderer worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"manifestRenderer",
		stepStart,
		"render kubernetes deployment manifests",
	)

	spec := normalizeProjectSpec(msg.Spec)
	imageTag := fmt.Sprintf("local/%s:%s", safeName(spec.Name), shortID(msg.OpID))
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate, OpCI:
		outcome, err = runManifestRendererApply(ctx, store, artifacts, msg, spec, imageTag)
	case OpDelete:
		outcome, err = runManifestRendererDelete(ctx, store, artifacts, msg)
	default:
		err = fmt.Errorf("unknown op kind: %s", msg.Kind)
	}
	if err != nil {
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(
			ctx,
			store,
			msg.OpID,
			"manifestRenderer",
			time.Now().UTC(),
			"",
			err.Error(),
			outcome.artifacts,
		)
		return res, err
	}

	_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "done", "")
	res.Message = outcome.message
	res.Artifacts = outcome.artifacts
	_ = markOpStepEnd(
		ctx,
		store,
		msg.OpID,
		"manifestRenderer",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runManifestRendererApply(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
) (repoBootstrapOutcome, error) {
	deployment := renderDeploymentManifest(spec, imageTag)
	service := renderServiceManifest(spec)
	renderedArtifacts, err := writeRenderedManifestFiles(
		artifacts,
		msg.ProjectID,
		deployment,
		service,
	)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: renderedArtifacts}, err
	}
	manifestsDir := manifestsRepoDir(artifacts, msg.ProjectID)
	repoErr := ensureLocalGitRepo(ctx, manifestsDir)
	if repoErr != nil {
		return repoBootstrapOutcome{message: "", artifacts: renderedArtifacts}, repoErr
	}
	_, commitErr := gitCommitIfChanged(
		ctx,
		manifestsDir,
		fmt.Sprintf("platform-sync: render manifests (%s)", shortID(msg.OpID)),
	)
	if commitErr != nil {
		return repoBootstrapOutcome{message: "", artifacts: renderedArtifacts}, commitErr
	}
	updateProjectReadyState(ctx, store, msg, spec)
	return repoBootstrapOutcome{
		message:   "rendered kubernetes deployment manifests",
		artifacts: renderedArtifacts,
	}, nil
}

func writeRenderedManifestFiles(
	artifacts ArtifactStore,
	projectID, deployment, service string,
) ([]string, error) {
	a1, err := artifacts.WriteFile(projectID, "deploy/deployment.yaml", []byte(deployment))
	if err != nil {
		return nil, err
	}
	a2, err := artifacts.WriteFile(projectID, "deploy/service.yaml", []byte(service))
	if err != nil {
		return []string{a1}, err
	}
	a3, err := artifacts.WriteFile(projectID, "repos/manifests/deployment.yaml", []byte(deployment))
	if err != nil {
		return []string{a1, a2}, err
	}
	a4, err := artifacts.WriteFile(projectID, "repos/manifests/service.yaml", []byte(service))
	if err != nil {
		return []string{a1, a2, a3}, err
	}
	return []string{a1, a2, a3, a4}, nil
}

func updateProjectReadyState(
	ctx context.Context,
	store *Store,
	msg ProjectOpMsg,
	spec ProjectSpec,
) {
	project, getErr := store.GetProject(ctx, msg.ProjectID)
	if getErr != nil {
		return
	}
	project.Spec = spec
	project.Status = ProjectStatus{
		Phase:      projectPhaseReady,
		UpdatedAt:  time.Now().UTC(),
		LastOpID:   msg.OpID,
		LastOpKind: string(msg.Kind),
		Message:    "ready",
	}
	_ = store.PutProject(ctx, project)
}

func runManifestRendererDelete(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (repoBootstrapOutcome, error) {
	writeDeleteAudit(artifacts, msg.ProjectID, msg.OpID)
	removeErr := artifacts.RemoveProject(msg.ProjectID)
	if removeErr != nil {
		return repoBootstrapOutcome{}, removeErr
	}
	_ = store.DeleteProject(ctx, msg.ProjectID)
	return repoBootstrapOutcome{
		message:   "project deleted and artifacts cleaned",
		artifacts: []string{},
	}, nil
}

func writeDeleteAudit(artifacts ArtifactStore, projectID, opID string) {
	auditDir := filepath.Join(filepath.Dir(artifacts.ProjectDir(projectID)), "_audit")
	_ = os.MkdirAll(auditDir, dirModePrivateRead)
	_ = os.WriteFile(
		filepath.Join(auditDir, fmt.Sprintf("%s.deleted.txt", projectID)),
		fmt.Appendf(
			nil,
			"project=%s deleted at %s op=%s\n",
			projectID,
			time.Now().UTC().Format(time.RFC3339),
			opID,
		),
		fileModePrivate,
	)
}

func shortID(id string) string {
	if len(id) <= shortIDLength {
		return id
	}
	return id[:shortIDLength]
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
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
	return first, spec.Environments[first].Vars
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

func markOpStepStart(
	ctx context.Context,
	store *Store,
	opID, worker string,
	startedAt time.Time,
	msg string,
) error {
	op, err := store.GetOp(ctx, opID)
	if err != nil {
		return err
	}
	op.Status = "running"
	op.Steps = append(op.Steps, OpStep{
		Worker:    worker,
		StartedAt: startedAt,
		EndedAt:   time.Time{},
		Message:   msg,
		Error:     "",
		Artifacts: nil,
	})
	return store.PutOp(ctx, op)
}

func markOpStepEnd(
	ctx context.Context,
	store *Store,
	opID, worker string,
	endedAt time.Time,
	message, stepErr string,
	artifacts []string,
) error {
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

func finalizeOp(
	ctx context.Context,
	store *Store,
	opID, projectID string,
	kind OperationKind,
	status, errMsg string,
) error {
	op, err := store.GetOp(ctx, opID)
	if err != nil {
		return err
	}
	op.Status = status
	op.Error = errMsg
	op.Finished = time.Now().UTC()
	putErr := store.PutOp(ctx, op)
	if putErr != nil {
		return putErr
	}

	// Best-effort: update project status (except delete where record might be removed later)
	p, err := store.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil
		}
		return err
	}
	switch {
	case kind == OpDelete && status == "running":
		p.Status.Phase = projectPhaseDel
	case status == "error":
		p.Status.Phase = projectPhaseError
		p.Status.Message = errMsg
	case status == "done":
		if kind != OpDelete {
			p.Status.Phase = projectPhaseReady
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
	apiLog := appLoggerForProcess().Source("api")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{
			ResponseWriter: w,
			status:         0,
		}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		dur := time.Since(started).Round(time.Millisecond)
		msg := fmt.Sprintf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, dur)
		switch {
		case rec.status >= httpServerErrThreshold:
			apiLog.Errorf("%s", msg)
		case rec.status >= httpClientErrThreshold:
			apiLog.Warnf("%s", msg)
		default:
			apiLog.Infof("%s", msg)
		}
	})
}

type RegistrationEvent struct {
	Action    string      `json:"action"` // create|update|delete
	ProjectID string      `json:"project_id,omitempty"`
	Spec      ProjectSpec `json:"spec"`
}

type SourceRepoWebhookEvent struct {
	ProjectID string `json:"project_id"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Ref       string `json:"ref,omitempty"` // e.g. refs/heads/main
	Commit    string `json:"commit,omitempty"`
}

func (a *API) createProjectFromSpec(
	ctx context.Context,
	spec ProjectSpec,
) (Project, Operation, WorkerResultMsg, error) {
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
	putErr := a.store.PutProject(ctx, p)
	if putErr != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, errors.New("failed to persist project")
	}

	op, final, err := a.runOp(ctx, OpCreate, projectID, spec)
	if err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}
	p, _ = a.store.GetProject(ctx, projectID)
	return p, op, final, nil
}

func (a *API) updateProjectFromSpec(
	ctx context.Context,
	projectID string,
	spec ProjectSpec,
) (Project, Operation, WorkerResultMsg, error) {
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
	putErr := a.store.PutProject(ctx, p)
	if putErr != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, errors.New("failed to persist project")
	}

	op, final, err := a.runOp(ctx, OpUpdate, projectID, spec)
	if err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}
	p, _ = a.store.GetProject(ctx, projectID)
	return p, op, final, nil
}

func (a *API) deleteProject(
	ctx context.Context,
	projectID string,
) (Operation, WorkerResultMsg, error) {
	p, err := a.store.GetProject(ctx, projectID)
	if err != nil {
		return Operation{}, WorkerResultMsg{}, err
	}
	p.Status.Phase = projectPhaseDel
	p.Status.Message = "queued delete"
	p.Status.UpdatedAt = time.Now().UTC()
	_ = a.store.PutProject(ctx, p)

	op, final, err := a.runOp(ctx, OpDelete, projectID, zeroProjectSpec())
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
	evt, err := decodeRegistrationEvent(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	switch evt.Action {
	case "create":
		a.handleRegistrationCreate(w, r, evt.Spec)
	case "update":
		a.handleRegistrationUpdate(w, r, evt.ProjectID, evt.Spec)
	case "delete":
		a.handleRegistrationDelete(w, r, evt.ProjectID)
	default:
		http.Error(w, "action must be create, update, or delete", http.StatusBadRequest)
	}
}

func decodeRegistrationEvent(r *http.Request) (RegistrationEvent, error) {
	var evt RegistrationEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		return RegistrationEvent{}, err
	}
	evt.Action = strings.TrimSpace(strings.ToLower(evt.Action))
	return evt, nil
}

func (a *API) handleRegistrationCreate(w http.ResponseWriter, r *http.Request, spec ProjectSpec) {
	project, op, final, err := a.createProjectFromSpec(r.Context(), spec)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	writeProjectOpFinalResponse(w, project, op, final)
}

func (a *API) handleRegistrationUpdate(
	w http.ResponseWriter,
	r *http.Request,
	projectID string,
	spec ProjectSpec,
) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	project, op, final, err := a.updateProjectFromSpec(r.Context(), projectID, spec)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	writeProjectOpFinalResponse(w, project, op, final)
}

func (a *API) handleRegistrationDelete(w http.ResponseWriter, r *http.Request, projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	op, final, err := a.deleteProject(r.Context(), projectID)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": true,
		"op":      op,
		"final":   final,
	})
}

func writeProjectOpFinalResponse(
	w http.ResponseWriter,
	project Project,
	op Operation,
	final WorkerResultMsg,
) {
	writeJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"op":      op,
		"final":   final,
	})
}

func writeRegistrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case isValidationError(err):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func isValidationError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "must") || strings.Contains(msg, "invalid")
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
	return normalizeBranchValue(branch) == branchMain || normalizeBranchValue(ref) == branchMain
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
		putErr := a.store.PutProject(r.Context(), p)
		if putErr != nil {
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
	projectID, ok := a.resolveProjectIDFromPath(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.handleProjectGetByID(w, r, projectID)
	case http.MethodPut:
		a.handleProjectUpdateByID(w, r, projectID)
	case http.MethodDelete:
		a.handleProjectDeleteByID(w, r, projectID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) resolveProjectIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		http.NotFound(w, r)
		return "", false
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	if rest == "" {
		http.NotFound(w, r)
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 1 {
		if parts[1] == "artifacts" {
			a.handleProjectArtifacts(w, r)
			return "", false
		}
		http.NotFound(w, r)
		return "", false
	}
	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return "", false
	}
	return projectID, true
}

func (a *API) handleProjectGetByID(w http.ResponseWriter, r *http.Request, projectID string) {
	project, ok := a.getProjectOrWriteError(w, r, projectID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (a *API) handleProjectUpdateByID(w http.ResponseWriter, r *http.Request, projectID string) {
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

	project, ok := a.getProjectOrWriteError(w, r, projectID)
	if !ok {
		return
	}
	project.Spec = spec
	project.Status.Phase = "Reconciling"
	project.Status.Message = "queued update"
	project.Status.UpdatedAt = time.Now().UTC()
	putErr := a.store.PutProject(r.Context(), project)
	if putErr != nil {
		http.Error(w, "failed to persist project", http.StatusInternalServerError)
		return
	}

	op, final, err := a.runOp(r.Context(), OpUpdate, projectID, spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	project, _ = a.store.GetProject(r.Context(), projectID)
	writeJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"op":      op,
		"final":   final,
	})
}

func (a *API) handleProjectDeleteByID(w http.ResponseWriter, r *http.Request, projectID string) {
	project, ok := a.getProjectOrWriteError(w, r, projectID)
	if !ok {
		return
	}
	project.Status.Phase = projectPhaseDel
	project.Status.Message = "queued delete"
	project.Status.UpdatedAt = time.Now().UTC()
	_ = a.store.PutProject(r.Context(), project)

	op, final, err := a.runOp(r.Context(), OpDelete, projectID, zeroProjectSpec())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": true,
		"op":      op,
		"final":   final,
	})
}

func (a *API) getProjectOrWriteError(
	w http.ResponseWriter,
	r *http.Request,
	projectID string,
) (Project, bool) {
	project, err := a.store.GetProject(r.Context(), projectID)
	if err == nil {
		return project, true
	}
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return Project{}, false
	}
	http.Error(w, "failed to read project", http.StatusInternalServerError)
	return Project{}, false
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
	if len(parts) == projectRelPathPartsMin {
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().
		Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(relPath)))
	http.ServeContent(w, r, filepath.Base(relPath), time.Time{}, bytes.NewReader(data))
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

func (a *API) runOp(
	ctx context.Context,
	kind OperationKind,
	projectID string,
	spec ProjectSpec,
) (Operation, WorkerResultMsg, error) {
	apiLog := appLoggerForProcess().Source("api")
	opID := newID()
	now := time.Now().UTC()

	// Persist initial op record
	op := Operation{
		ID:        opID,
		Kind:      kind,
		ProjectID: projectID,
		Requested: now,
		Finished:  time.Time{},
		Status:    "queued",
		Error:     "",
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
		Err:       "",
		At:        now,
	}
	b, _ := json.Marshal(msg)
	startSubject := subjectProjectOpStart
	if kind == OpCI {
		startSubject = subjectBootstrapDone
	}
	finalizeCtx := context.WithoutCancel(ctx)
	if err := a.nc.Publish(startSubject, b); err != nil {
		_ = finalizeOp(finalizeCtx, a.store, opID, projectID, kind, "error", err.Error())
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
		_ = finalizeOp(
			finalizeCtx,
			a.store,
			opID,
			projectID,
			kind,
			"error",
			"timeout waiting for workers",
		)
		apiLog.Errorf("timeout op=%s kind=%s project=%s", opID, kind, projectID)
		return Operation{}, WorkerResultMsg{}, errors.New("timeout waiting for workers")
	case final = <-ch:
	}

	if final.Err != "" {
		_ = finalizeOp(finalizeCtx, a.store, opID, projectID, kind, "error", final.Err)
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
	mainLog := appLoggerForProcess().Source("main")
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

	// 3) Artifacts root
	artifacts := NewFSArtifacts(defaultArtifactsRoot)
	mkdirErr := os.MkdirAll(defaultArtifactsRoot, dirModePrivateRead)
	if mkdirErr != nil {
		mainLog.Fatalf("mkdir artifacts root: %v", mkdirErr)
	}

	// 4) Start worker pipeline
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

	// 5) Subscribe to final results and multiplex to HTTP requests
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
