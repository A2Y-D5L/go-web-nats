package platform

import (
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
)

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
