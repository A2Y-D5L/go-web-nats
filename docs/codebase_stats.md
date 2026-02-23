# Codebase Statistics Report

**Project:** `go-web-nats`  
**Go Version:** 1.25.0  
**Generated:** February 22, 2026

---

## Summary

| Metric | Value |
| ------ | ----- |
| **Total Lines of Code** | ~29636 |
| **Total Source Files** | 85 |
| **Primary Language** | Go |
| **Architecture** | HTTP API + Embedded Web App + NATS Backend |

---

## Lines of Code by Type

| File Type | Lines | Files | Description |
| --------- | ----- | ----- | ----------- |
| **Go (Production)** | 13017 | 42 | Backend application code |
| **Go (Tests)** | 7413 | 23 | Unit and integration tests |
| **JavaScript** | 4953 | 7 | Frontend web application |
| **HTML** | 540 | 1 | Web UI template |
| **CSS** | 1366 | 1 | Styling |
| **Markdown** | 1913 | 6 | Documentation |
| **YAML** | 194 | 3 | Configuration |
| **JSON** | 240 | 2 | Schema definitions |

---

## Go Code Analysis

| Metric | Count |
| ------ | ----- |
| **Functions/Methods** | 691 |
| **Structs** | 104 |
| **Interfaces** | 3 |
| **Test Functions** | 100 |
| **Test Coverage Ratio** | 36% of Go code is tests |

### Top 15 Largest Go Files

| File | Lines | Category |
| ---- | ----- | -------- |
| `api_projects.go` | 1552 | API |
| `workers_action_promotion.go` | 1285 | Workers |
| `api_processes.go` | 1171 | API |
| `api_async_ops_test.go` | 904 | Test |
| `store.go` | 899 | Storage |
| `workers_loop_test.go` | 810 | Test |
| `workers_loop.go` | 681 | Workers |
| `workers_action_deploy.go` | 632 | Workers |
| `api_project_ops_history_test.go` | 583 | Test |
| `op_events.go` | 551 | Events |
| `api_runop.go` | 490 | API |
| `api_promotion_preview_test.go` | 441 | Test |
| `internal_testbridge_test.go` | 438 | Test |
| `workers_render.go` | 436 | Workers |
| `api_project_releases_test.go` | 436 | Test |

---

## Dependencies

| Type | Count |
| ---- | ----- |
| **Direct Dependencies** | 7 |
| **Indirect Dependencies** | 100 |
| **Total** | 107 |

### Key Direct Dependencies

| Package | Purpose |
| ------- | ------- |
| `github.com/cyphar/filepath-securejoin` | Secure path handling |
| `github.com/go-git/go-git/v5` | Git operations |
| `github.com/moby/buildkit` | Container image building |
| `github.com/nats-io/nats-server/v2` | Embedded NATS server |
| `github.com/nats-io/nats.go` | NATS client |
| `sigs.k8s.io/kustomize/api` | Kubernetes manifest rendering |
| `sigs.k8s.io/kustomize/kyaml` | YAML processing |

---

## API Endpoints

| Endpoint | Description |
| -------- | ----------- |
| `GET/POST /api/projects` | Project management |
| `GET/PUT/DELETE /api/projects/{id}` | Project by ID |
| `GET /api/ops/{id}` | Operation details |
| `GET /api/events/registration` | SSE registration events |
| `GET /api/events/deployment` | SSE deployment events |
| `GET /api/events/promotion` | SSE promotion events |
| `GET /api/events/promotion/preview` | Promotion preview events |
| `GET /api/events/release` | Release events |
| `POST /api/webhooks/source` | Source repo webhooks |
| `GET /api/system` | System info |
| `GET /api/healthz` | Health check |

---

## NATS Messaging Subjects

| Subject | Purpose |
| ------- | ------- |
| `paas.project.op.start` | Project operation initiation |
| `paas.project.op.registration.done` | Registration completion |
| `paas.project.op.bootstrap.done` | Bootstrap completion |
| `paas.project.op.build.done` | Build completion |
| `paas.project.op.deploy.done` | Deploy completion |
| `paas.project.process.deployment.start` | Deployment process start |
| `paas.project.process.deployment.done` | Deployment process done |
| `paas.project.process.promotion.start` | Promotion process start |
| `paas.project.process.promotion.done` | Promotion process done |

### KV Buckets

| Bucket | Purpose |
| ------ | ------- |
| `paas_projects` | Project state storage |
| `paas_ops` | Operation & release storage |

---

## Project Structure

```
├── cmd/server/          # Server entrypoint
├── web/                 # Frontend (JS/HTML/CSS)
│   ├── app.js
│   ├── app_core.js
│   ├── app_data_artifacts.js
│   ├── app_events.js
│   ├── app_flow.js
│   ├── app_render_projects_ops.js
│   ├── app_render_surfaces.js
│   ├── index.html
│   └── styles.css
├── cfg/                 # Configuration examples
├── docs/                # API contracts & documentation
│   └── project-schema/  # JSON schema definitions
├── scripts/             # Build/task scripts
└── *.go                 # Core application (root)
```

---

## JavaScript Analysis

| Metric | Value |
| ------ | ----- |
| **Functions** | ~238 |
| **Files** | 7 |
| **Architecture** | Single-page app with modular JS |

### Frontend Files

| File | Description |
| ---- | ----------- |
| `app.js` | Main application entry |
| `app_core.js` | Core utilities |
| `app_flow.js` | Flow/state management |
| `app_events.js` | Event handling |
| `app_data_artifacts.js` | Artifact data layer |
| `app_render_projects_ops.js` | Project/ops rendering |
| `app_render_surfaces.js` | Surface rendering |

---

## Build System

The project uses a Makefile with 20+ targets:

### Testing

- `test` - Run unit tests
- `test-race` - Run tests with race detector
- `test-api` - Run API-focused tests
- `test-workers` - Run worker-focused tests
- `test-store` - Run store/artifact-focused tests
- `test-model` - Run model/spec-focused tests
- `cover` - Run tests with coverage report

### Code Quality

- `fmt` - Format Go files with gofumpt
- `fmt-check` - Fail if Go formatting is not gofumpt-clean
- `vet` - Run go vet
- `lint` - Run golangci-lint
- `lint-fix` - Run golangci-lint with auto-fix

### Utilities

- `tidy` - Sync Go module dependencies
- `tools` - Verify local toolchain dependencies
- `js-check` - Syntax-check frontend JS
- `agent-check` - Validate agent context files
- `task-list` - List task IDs from TASKMAP.yaml
- `task-show` - Show files/tests for a task

---

## Git Statistics

| Metric | Value |
| ------ | ----- |
| **Total Commits** | 44 |
| **Contributors** | 1 |
| **Commits in 2026** | 44 |
| **Default Branch** | main |

---

## Code Distribution

```
Go Production:  43.9%  ███████████████████░░░░░░░░░░░░░░░░░░░░░░░░░░
Go Tests:       25.0%  ███████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
JavaScript:     16.7%  ███████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
Markdown:        6.4%  ██░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
CSS:             4.6%  ██░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
HTML:            1.8%  ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
Config:          1.4%  ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
```
