# EmbeddedWebApp-HTTPAPI-BackendNATS

Single-binary Go demo platform:

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

## What It Demonstrates

- Registration-driven project lifecycle (`create`, `update`, `delete`)
- Source-repo webhook driven CI (`main` branch only)
- Real local git repo bootstrapping for source + manifests repos
- Local git hooks in source repo posting webhook events back to the API server
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
    "name": "demo-app",
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

## Local Repos And Hooks

On registration create/update, `repoBootstrap` now creates real local repos at:

- `data/artifacts/<project-id>/repos/source`
- `data/artifacts/<project-id>/repos/manifests`

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
| `POST` | `/api/webhooks/source` | Source repo webhook API |
| `GET` | `/api/ops/{opID}` | Operation details |
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
- `build/Dockerfile`
- `build/publish-local-daemon.json`
- `deploy/deployment.yaml`
- `deploy/service.yaml`

## Run Locally

Prereqs:

- Go `1.25+` (module target is `1.25.0`)

Commands:

```bash
go mod tidy
go test ./...
go run ./cmd/server
```

Open:

- `http://127.0.0.1:8080`

After creating a project, make a commit in the bootstrapped source repo to trigger CI locally:

```bash
cd data/artifacts/<project-id>/repos/source
git switch main
echo "// local change" >> main.go
git add main.go
git commit -m "feat: local source change"
```

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
      "name":"demo-app",
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
