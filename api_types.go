package platform

import (
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

type API struct {
	nc        *nats.Conn
	store     *Store
	artifacts ArtifactStore
	waiters   *waiterHub
	opEvents  *opEventHub

	opHeartbeatInterval time.Duration

	runtimeVersion              string
	runtimeHTTPAddr             string
	runtimeArtifactsRoot        string
	runtimeBuilderMode          imageBuilderModeResolution
	runtimeCommitWatcherEnabled bool
	runtimeNATSEmbedded         bool
	runtimeNATSStoreDir         string
	runtimeNATSStoreEphemeral   bool

	sourceTriggerMu     sync.Mutex
	projectStartLocksMu sync.Mutex
	projectStartLocks   map[string]*sync.Mutex
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
	mux.HandleFunc("/api/events/deployment", a.handleDeploymentEvents)
	mux.HandleFunc("/api/events/promotion/preview", a.handlePromotionPreviewEvents)
	mux.HandleFunc("/api/events/promotion", a.handlePromotionEvents)
	mux.HandleFunc("/api/events/release", a.handleReleaseEvents)
	mux.HandleFunc("/api/events/rollback/preview", a.handleRollbackPreviewEvents)
	mux.HandleFunc("/api/events/rollback", a.handleRollbackEvents)
	mux.HandleFunc("/api/webhooks/source", a.handleSourceRepoWebhook)
	mux.HandleFunc("/api/system", a.handleSystem)
	mux.HandleFunc("/api/healthz", a.handleHealthz)

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

func (s *statusRecorder) Flush() {
	flusher, ok := s.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
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

type DeploymentEvent struct {
	ProjectID   string `json:"project_id"`
	Environment string `json:"environment,omitempty"`
}

type PromotionEvent struct {
	ProjectID string `json:"project_id"`
	FromEnv   string `json:"from_env"`
	ToEnv     string `json:"to_env"`
}

type ReleaseEvent struct {
	ProjectID string `json:"project_id"`
	FromEnv   string `json:"from_env"`
	ToEnv     string `json:"to_env,omitempty"`
}

type RollbackEvent struct {
	ProjectID   string        `json:"project_id"`
	Environment string        `json:"environment"`
	ReleaseID   string        `json:"release_id"`
	Scope       RollbackScope `json:"scope"`
	Override    bool          `json:"override,omitempty"`
}

type TransitionPreviewGate struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Status string `json:"status"` // passed | blocked | warning
	Detail string `json:"detail,omitempty"`
}

type TransitionPreviewBlocker struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Why        string `json:"why"`
	NextAction string `json:"next_action"`
}

type TransitionPreviewRelease struct {
	ID            string        `json:"id"`
	Environment   string        `json:"environment"`
	Image         string        `json:"image,omitempty"`
	OpKind        OperationKind `json:"op_kind,omitempty"`
	DeliveryStage DeliveryStage `json:"delivery_stage,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
}

type PromotionPreviewResponse struct {
	Action        string                     `json:"action"` // promote | release
	SourceRelease *TransitionPreviewRelease  `json:"source_release,omitempty"`
	TargetRelease *TransitionPreviewRelease  `json:"target_release,omitempty"`
	ChangeSummary string                     `json:"change_summary"`
	Gates         []TransitionPreviewGate    `json:"gates"`
	Blockers      []TransitionPreviewBlocker `json:"blockers"`
	RolloutPlan   []string                   `json:"rollout_plan"`
}

type ReleaseCompareResponse struct {
	FromID        string              `json:"from_id"`
	ToID          string              `json:"to_id"`
	FromRelease   *ReleaseRecord      `json:"from_release,omitempty"`
	ToRelease     *ReleaseRecord      `json:"to_release,omitempty"`
	Summary       string              `json:"summary"`
	ImageDelta    ReleaseCompareDelta `json:"image_delta"`
	ConfigDelta   ReleaseCompareDelta `json:"config_delta"`
	RenderedDelta ReleaseCompareDelta `json:"rendered_delta"`
}

type ReleaseCompareDelta struct {
	Changed bool     `json:"changed"`
	From    string   `json:"from,omitempty"`
	To      string   `json:"to,omitempty"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Updated []string `json:"updated,omitempty"`
}

type RollbackPreviewResponse struct {
	ProjectID      string                     `json:"project_id"`
	Environment    string                     `json:"environment"`
	ReleaseID      string                     `json:"release_id"`
	Scope          RollbackScope              `json:"scope"`
	Override       bool                       `json:"override,omitempty"`
	Ready          bool                       `json:"ready"`
	SourceRelease  *TransitionPreviewRelease  `json:"source_release,omitempty"`
	CurrentRelease *TransitionPreviewRelease  `json:"current_release,omitempty"`
	Compare        *ReleaseCompareResponse    `json:"compare,omitempty"`
	Gates          []TransitionPreviewGate    `json:"gates"`
	Blockers       []TransitionPreviewBlocker `json:"blockers"`
}
