# EmbeddedWebApp-HTTPAPI-BackendNATS

Single-binary Go PaaS platform:

- embeds a web frontend
- runs an HTTP API
- starts an embedded NATS+JetStream backend
- executes project operations through a worker pipeline

## Agent-First Context Files

- `AGENTS.md`: primary operational contract for coding agents
- `CODEMAP.md`: code ownership and entrypoint map
- `TASKMAP.yaml`: machine-readable task-to-file mapping
- `docs/AGENT_PLAYBOOK.md`: concrete task recipes
- `docs/API_CONTRACTS.md`: canonical request/response contract reference

Quick task scoping commands:

- `make task-list`
- `make task-show TASK=api.webhooks`
- `make task-files TASK=workers.bootstrap`
- `make task-tests TASK=workers.runtime`
- `make task-audit TASK=api.webhooks`

## Core Platform Capabilities

- Registration-driven project lifecycle (`create`, `update`, `delete`)
- Source-repo webhook driven CI (`main` branch only)
- Real local git repo bootstrapping for source + manifests repos
- Local git hooks in source repo posting webhook events back to the API server
- Optional in-process source commit watcher (can run alongside hooks)
- Worker orchestration over NATS subjects
- Persistent project/operation state in JetStream KV
- Artifact generation and browsing from the UI

## Current Pipeline

Workers are named by responsibility:

1. `registrar`
2. `repoBootstrap`
3. `imageBuilder`
4. `manifestRenderer`

Operation flow:

- Registration operations (`create`, `update`, `delete`) run the full chain.
- CI operations (`ci`) start at `imageBuilder` and then `manifestRenderer`.

## Two API Pathways

### 1) Registration events (frontend pathway)

The frontend sends:

- `POST /api/events/registration`

Payload:

```json
{
  "action": "create",
  "project_id": "optional-for-create",
  "spec": {
    "apiVersion": "platform.example.com/v2",
    "kind": "App",
    "name": "platform-app",
    "runtime": "go_1.26",
    "capabilities": ["http", "metrics"],
    "environments": {
      "dev": { "vars": { "LOG_LEVEL": "info" } },
      "prod": { "vars": { "LOG_LEVEL": "warn" } }
    },
    "networkPolicies": {
      "ingress": "internal",
      "egress": "internal"
    }
  }
}
```

`action` supports: `create`, `update`, `delete`.

Registration triggers are async:

- handlers return `202 Accepted` with `accepted: true` and an `op` reference
- clients should monitor operation progress via SSE (`/api/ops/{opID}/events`) or fallback polling (`/api/ops/{opID}`)

### 2) Source webhook events (repo pathway)

Webhook endpoint:

- `POST /api/webhooks/source`

Payload:

```json
{
  "project_id": "<project-id>",
  "repo": "source",
  "branch": "main",
  "ref": "refs/heads/main",
  "commit": "abc123"
}
```

Rules:

- only source repo webhooks are accepted (`repo` omitted or `source`)
- only `main` branch triggers CI (supports `main`, `heads/main`, `refs/heads/main`)
- accepted events trigger pipeline kind `ci`
- accepted responses are `202 Accepted` and include operation metadata

## Delivery Transitions

Process endpoints:

- `POST /api/events/deployment` deploys to `dev` only.
- `POST /api/events/promotion` handles environment-to-environment promotion.
- `POST /api/events/release` handles promotion into production (`prod`/`production`).

Lifecycle classification:

- Non-production target environment => `promote`.
- Production target environment => `release`.

Deployment/promotion/release event handlers are async and return `202 Accepted` with an `op` reference.

## Realtime Operation Streaming

Operation state is available through:

- `GET /api/ops/{opID}` for snapshot polling
- `GET /api/ops/{opID}/events` for SSE streaming (`op.bootstrap`, `op.status`, `step.*`, `op.completed`/`op.failed`, `op.heartbeat`)

SSE supports reconnect replay via `Last-Event-ID` against a bounded in-memory event history, and falls back to an authoritative `op.bootstrap` snapshot rebuilt from persisted operation state after process restarts.

## Local Repos And Hooks

On registration create/update, `repoBootstrap` now creates real local repos at:

- `<artifacts-root>/<project-id>/repos/source`
- `<artifacts-root>/<project-id>/repos/manifests`

Default `<artifacts-root>` is OS-aware and outside the module tree:

- macOS: `~/Library/Application Support/EmbeddedWebApp-HTTPAPI-BackendNATS/artifacts`
- Linux: `$XDG_STATE_HOME/EmbeddedWebApp-HTTPAPI-BackendNATS/artifacts`
- Linux fallback when `XDG_STATE_HOME` is unset: `~/.local/state/EmbeddedWebApp-HTTPAPI-BackendNATS/artifacts`

The source repo gets local hooks:

- `.git/hooks/post-commit`
- `.git/hooks/post-merge`

Hook behavior:

- triggers only on branch `main`
- sends `POST /api/webhooks/source` to local API
- ignores platform-managed commits with subject prefix `platform-sync:`

Hook endpoint defaults to:

- `http://127.0.0.1:8080/api/webhooks/source`

Optional override:

- `PAAS_LOCAL_API_BASE_URL` (example: `http://127.0.0.1:8080`)
- `PAAS_ARTIFACTS_ROOT` (optional explicit artifact root override)
- `PAAS_ENABLE_COMMIT_WATCHER` (`true|false`, default `false`) enables in-process polling watcher for source commits
- `PAAS_IMAGE_BUILDER_MODE` (`artifact|buildkit`, default `buildkit`)
- `PAAS_BUILDKIT_ADDR` (optional, default `unix:///run/buildkit/buildkitd.sock` when BuildKit mode is enabled)
- `PAAS_NATS_STORE_DIR` (default `./data/nats`; set to `temp` or `ephemeral` for prior temp-dir behavior)

NATS/JetStream state persistence:

- By default, embedded NATS reuses `./data/nats`, so project and operation KV state survives app restarts.
- Older behavior used a temp JetStream dir removed on shutdown.
- To force ephemeral runtime state, set `PAAS_NATS_STORE_DIR=temp` (or `ephemeral`).

Image builder mode behavior:

- `buildkit`: runs the image builder backend through BuildKit integration without shelling out to `docker`, `buildctl`, or `buildkitd`.
  - BuildKit metadata/debug artifacts are written under `build/`:
    - `build/buildkit-summary.txt`
    - `build/buildkit-metadata.json`
    - `build/buildkit.log`
- `artifact`: preserves prior metadata-only behavior and writes build artifacts without a container runtime build.

Startup resolution policy:

- Requested mode defaults to `buildkit` when `PAAS_IMAGE_BUILDER_MODE` is unset.
- Startup resolves an effective mode:
  - If `buildkit` was implicitly requested and BuildKit capability or daemon reachability is unavailable, runtime auto-falls back to `artifact`.
  - If `buildkit` was explicitly requested (`PAAS_IMAGE_BUILDER_MODE=buildkit`) and unavailable, build operations return a clear policy error.
- Startup logs always include requested mode, effective mode, and fallback/policy reason when present.
- Build publish metadata (`build/publish-local-daemon.json`) includes requested/effective builder mode fields.
- BuildKit-tagged builds require Moby BuildKit Go modules to be available in the module graph/cache for this environment.

Trigger dedupe:

- webhook and watcher paths share commit-level dedupe state per project
- duplicate commit notifications are ignored with reason `ignored: commit already processed`

## API Summary

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/` | UI |
| `GET` | `/api/projects` | List projects |
| `GET` | `/api/projects/{id}` | Get project |
| `PUT` | `/api/projects/{id}` | Legacy direct update |
| `DELETE` | `/api/projects/{id}` | Legacy direct delete |
| `POST` | `/api/projects` | Legacy direct create |
| `POST` | `/api/events/registration` | Registration event API |
| `POST` | `/api/events/deployment` | Dev deployment API |
| `POST` | `/api/events/promotion` | Promotion/release transition API |
| `POST` | `/api/events/release` | Explicit release API |
| `POST` | `/api/webhooks/source` | Source repo webhook API |
| `GET` | `/api/ops/{opID}` | Operation details |
| `GET` | `/api/ops/{opID}/events` | Operation realtime event stream (SSE) |
| `GET` | `/api/projects/{id}/artifacts` | List artifact files |
| `GET` | `/api/projects/{id}/artifacts/{path...}` | Download artifact file |

## Project Spec Source

Config contracts are modeled from:

- `cfg/project-example.yaml`
- `cfg/project-jsonschema.json`

## Artifact Outputs (examples)

- `registration/project.yaml`
- `registration/registration.json`
- `repos/source/...`
- `repos/manifests/...`
- `repos/manifests/kustomization.yaml`
- `repos/manifests/rendered.yaml`
- `build/Dockerfile`
- `build/publish-local-daemon.json`
- `build/image.txt`
- `build/buildkit-summary.txt` (BuildKit mode)
- `build/buildkit-metadata.json` (BuildKit mode)
- `build/buildkit.log` (BuildKit mode)
- `deploy/<env>/deployment.yaml`
- `deploy/<env>/service.yaml`
- `deploy/<env>/rendered.yaml`
- `promotions/<from>-to-<to>/rendered.yaml`
- `releases/<from>-to-<to>/rendered.yaml`

## Frontend UX Highlights

The embedded UI (`/`) now mirrors backend execution semantics directly:

- system strip with project/health/op/build mode status
- always-visible search/filter/sort controls for project inventory
- selected-project action workspace for create/update/delete + source webhook CI
- one-click `Build latest source` primary action with advanced webhook payload overrides in an expandable section
- explicit dev deploy with promotion/release transition guardrails
- live operation timeline streamed by SSE with polling fallback
- artifact explorer with preview/download, BuildKit metadata signal, and imageBuilder output visibility

Keyboard shortcuts:

- `/` focuses project search
- `r` refreshes projects
- `a` loads artifacts for the selected project

## Run Locally

Prereqs:

- Go `1.25+` (module target is `1.25.0`)
- `git` and `curl` (used by local hook + webhook loopback flow)

Recommended preflight:

```bash
go mod tidy
make check
```

Makefile shortcuts:

```bash
make setup-local
make setup-buildkit
```

macOS note:

- `make setup-buildkit` may install `buildctl` without `buildkitd` (daemon) because Darwin release archives can omit `buildkitd`.
- In that case, run a Linux BuildKit daemon via container:

```bash
make buildkit-start-container
```

Then use:

```bash
BUILDKIT_ADDR=tcp://127.0.0.1:1234 make buildkit-check
BUILDKIT_ADDR=tcp://127.0.0.1:1234 make run-buildkit
```

### Option A: Local-Dev Reliable Startup (explicit artifact mode)

Use this when you want predictable local behavior without BuildKit runtime requirements:

```bash
PAAS_IMAGE_BUILDER_MODE=artifact make run
```

Equivalent:

```bash
PAAS_IMAGE_BUILDER_MODE=artifact go run ./cmd/server
```

### Option B: BuildKit Mode (explicit buildkit mode)

Use this when you want real image builds through the in-process BuildKit backend:

```bash
go run -tags buildkit ./cmd/server
```

Optional BuildKit endpoint override:

```bash
PAAS_BUILDKIT_ADDR=unix:///run/buildkit/buildkitd.sock go run -tags buildkit ./cmd/server
```

Equivalent Make target:

```bash
make run-buildkit
```

### BuildKit Dependency Setup (local runtime/build capability)

The runtime BuildKit path requires both:

- BuildKit-enabled binary: run with `-tags buildkit`
- Reachable BuildKit daemon API endpoint: configured via `PAAS_BUILDKIT_ADDR`

1. Install BuildKit daemon locally.
Examples:

```bash
# macOS/Linux (Homebrew provides buildctl)
brew install buildkit
```

```bash
# Install buildkitd (daemon) from official release binaries
BK_VER=v0.27.1
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"      # darwin or linux
ARCH="$(uname -m)"                                   # arm64 or x86_64
if [ "$ARCH" = "x86_64" ]; then ARCH=amd64; fi
curl -fsSL -o /tmp/buildkit.tgz \
  "https://github.com/moby/buildkit/releases/download/${BK_VER}/buildkit-${BK_VER}.${OS}-${ARCH}.tar.gz"
tar -xzf /tmp/buildkit.tgz -C /tmp
mkdir -p "$HOME/.local/bin"
install -m 0755 /tmp/bin/buildkitd "$HOME/.local/bin/buildkitd"
install -m 0755 /tmp/bin/buildctl "$HOME/.local/bin/buildctl"
```

2. Ensure your shell can find the daemon binary.
Example (zsh):

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
command -v buildkitd
```

If `go install github.com/moby/buildkit/cmd/buildkitd@latest` fails with
`go.mod ... contains one or more exclude directives` (seen on Go `1.26.x`),
use the release-binary method above.

3. Start a local BuildKit daemon and choose a socket address.
Example (user-owned socket):

```bash
mkdir -p "$HOME/.local/run/buildkit"
buildkitd --addr "unix://$HOME/.local/run/buildkit/buildkitd.sock"
```

Equivalent Make target:

```bash
make buildkit-start
```

4. Point the server at that endpoint and run with BuildKit support:

```bash
PAAS_BUILDKIT_ADDR="unix://$HOME/.local/run/buildkit/buildkitd.sock" go run -tags buildkit ./cmd/server
```

5. If BuildKit cannot be provided locally, use artifact mode explicitly:

```bash
PAAS_IMAGE_BUILDER_MODE=artifact go run ./cmd/server
```

Notes:

- Default requested mode is `buildkit`, but startup auto-falls back to `artifact` when BuildKit is not available.
- `PAAS_IMAGE_BUILDER_MODE=buildkit` preserves explicit BuildKit intent and returns clear policy errors if support is unavailable.

After startup, open:

- `http://127.0.0.1:8080`

Optional smoke flow (with server running):

```bash
make wait-api
make smoke-registration
```

After creating a project, make a commit in the bootstrapped source repo to trigger CI locally:

```bash
cd "<artifacts-root>/<project-id>/repos/source"
git switch main
echo "// local change" >> main.go
git add main.go
git commit -m "feat: local source change"
```

`<artifacts-root>` is printed on startup (`Artifacts root: ...`). You can pin it with `PAAS_ARTIFACTS_ROOT`.

## Quick cURL Examples

Create via registration event:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/events/registration \
  -H 'content-type: application/json' \
  -d '{
    "action":"create",
    "spec":{
      "apiVersion":"platform.example.com/v2",
      "kind":"App",
      "name":"platform-app",
      "runtime":"go_1.26",
      "capabilities":["http"],
      "environments":{"dev":{"vars":{"LOG_LEVEL":"info"}}},
      "networkPolicies":{"ingress":"internal","egress":"internal"}
    }
  }'
```

Trigger CI via source webhook:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/webhooks/source \
  -H 'content-type: application/json' \
  -d '{
    "project_id":"<project-id>",
    "repo":"source",
    "branch":"main",
    "commit":"abc123"
  }'
```

Inspect operations:

```bash
curl -sS http://127.0.0.1:8080/api/ops/<op-id>
```
