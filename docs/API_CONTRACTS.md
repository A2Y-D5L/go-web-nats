# API Contracts

Canonical request/response contracts for the local API.

## Registration Events

Endpoint:

- `POST /api/events/registration`

Request body:

```json
{
  "action": "create | update | delete",
  "project_id": "required for update/delete",
  "spec": {
    "apiVersion": "platform.example.com/v2",
    "kind": "App",
    "name": "platform-app",
    "runtime": "go_1.26",
    "capabilities": ["http"],
    "environments": {
      "dev": { "vars": { "LOG_LEVEL": "info" } }
    },
    "networkPolicies": {
      "ingress": "internal | none",
      "egress": "internal | none"
    }
  }
}
```

Rules:

- `action` must be one of `create`, `update`, `delete`.
- `project_id` is required for `update` and `delete`.
- `spec` is validated for `create` and `update`; it is ignored for `delete`.
- Trigger behavior is async: the API enqueues work and returns immediately with operation metadata.

Success (`create` / `update`) response:

- Status: `202 Accepted`

```json
{
  "accepted": true,
  "project": {},
  "op": {}
}
```

Success (`delete`) response:

- Status: `202 Accepted`

```json
{
  "accepted": true,
  "deleted": false,
  "project_id": "project-id",
  "op": {}
}
```

Conflict (`create`/`update`/`delete`/any project op trigger while the same project has a queued or running op):

- Status: `409 Conflict`

```json
{
  "accepted": false,
  "reason": "project already has an active operation (...)",
  "project_id": "project-id",
  "requested_kind": "create | update | delete | ci | deploy | promote | release",
  "active_op": { "id": "op-id", "kind": "deploy", "status": "running" },
  "next_step": "wait for the active operation to reach done or error, then retry"
}
```

Create enqueue/publish failure (recoverable metadata):

- Status: `500 Internal Server Error`

```json
{
  "accepted": false,
  "reason": "publish op: ...",
  "project_id": "project-id",
  "requested_kind": "create",
  "op_id": "op-id",
  "project_rolled_back": true,
  "next_step": "retry create request"
}
```

Notes:

- `op_id` is included when operation persistence succeeded before publish failed.
- `project_rolled_back=true` means no durable new project record remains after the failure.
- Clients should use `next_step` to drive retry/recovery UX.

## Source Webhooks

Endpoint:

- `POST /api/webhooks/source`

Request body:

```json
{
  "project_id": "project-id",
  "repo": "source",
  "branch": "main",
  "ref": "refs/heads/main",
  "commit": "abc123"
}
```

Behavior:

- Only source repo events are accepted (`repo` omitted or `source`).
- Only `main` branch events trigger CI.
- Accepted events enqueue operation kind `ci`.
- Duplicate commit events for the same project are ignored (`reason: "ignored: commit already processed"`).

Accepted response:

- Status: `202 Accepted`

```json
{
  "accepted": true,
  "reason": "",
  "trigger": "source.main.webhook | source.main.watcher",
  "project": "project-id",
  "op": {},
  "commit": "abc123"
}
```

Ignored response:

- Status: `202 Accepted`

```json
{
  "accepted": false,
  "reason": "ignored: ...",
  "trigger": "source.main.webhook | source.main.watcher",
  "project": "project-id",
  "op": null,
  "commit": "abc123"
}
```

Conflict response (project has a queued/running operation):

- Status: `409 Conflict`

```json
{
  "accepted": false,
  "reason": "project already has an active operation (...)",
  "project_id": "project-id",
  "requested_kind": "ci",
  "active_op": { "id": "op-id", "kind": "deploy", "status": "running" },
  "next_step": "wait for the active operation to reach done or error, then retry"
}
```

## Deployment Events

Endpoint:

- `POST /api/events/deployment`

Request body:

```json
{
  "project_id": "project-id",
  "environment": "dev"
}
```

Rules:

- `project_id` is required.
- `environment` is optional and defaults to `dev`.
- Only `dev` is accepted by deployment events; higher environments must use promotion or release transitions.

Success response:

- Status: `202 Accepted`

```json
{
  "accepted": true,
  "project": {},
  "op": {}
}
```

Conflict response (project has a queued/running operation):

- Status: `409 Conflict`
- Body uses the same shape as the conflict response above.

## Promotion Events

Endpoint:

- `POST /api/events/promotion`

Request body:

```json
{
  "project_id": "project-id",
  "from_env": "dev",
  "to_env": "staging"
}
```

Rules:

- `project_id`, `from_env`, and `to_env` are required.
- `from_env` and `to_env` must differ.
- Both environments must be defined for the project (except `dev`, which is always supported for deployment/promotion/release state).
- If `to_env` is production (`prod` or `production`), the operation is classified as `release` (not `promote`).

Success response:

- Status: `202 Accepted`

```json
{
  "accepted": true,
  "project": {},
  "op": {}
}
```

Conflict response (project has a queued/running operation):

- Status: `409 Conflict`
- Body uses the same shape as the conflict response above.

## Release Events

Endpoint:

- `POST /api/events/release`

Request body:

```json
{
  "project_id": "project-id",
  "from_env": "staging",
  "to_env": "prod"
}
```

Rules:

- `project_id` and `from_env` are required.
- `to_env` is optional and defaults to `prod`.
- `to_env` must resolve to a production environment (`prod` or `production`) defined for the project.
- `from_env` and `to_env` must differ.

Success response:

- Status: `202 Accepted`

```json
{
  "accepted": true,
  "project": {},
  "op": {}
}
```

Conflict response (project has a queued/running operation):

- Status: `409 Conflict`
- Body uses the same shape as the conflict response above.

## System Status

Endpoint:

- `GET /api/system`

Purpose:

- Returns runtime capability state so clients can render system transport/builder details without inferring from operation history or artifacts.

Response:

```json
{
  "version": "v0.0.0",
  "http_addr": "127.0.0.1:8080",
  "artifacts_root": "/path/to/artifacts",
  "builder_mode_requested": "buildkit",
  "builder_mode_effective": "artifact",
  "builder_mode_reason": "buildkit support is unavailable in this binary",
  "commit_watcher_enabled": false,
  "nats": {
    "embedded": true,
    "store_dir": "./data/nats",
    "store_dir_mode": "persistent | ephemeral"
  },
  "realtime": {
    "sse_enabled": true,
    "sse_replay_window": 256,
    "sse_heartbeat_interval": "10s"
  },
  "time": "2026-02-22T12:34:56Z"
}
```

Notes:

- `builder_mode_reason` is included when requested/effective mode differ.
- `realtime.sse_replay_window` is event-count based.
- `time` is server UTC.

## Health Probe

Endpoint:

- `GET /api/healthz`

Response:

```json
{
  "ok": true,
  "time": "2026-02-22T12:34:56Z"
}
```

## Projects

Endpoints:

- `GET /api/projects`
- `POST /api/projects`
- `GET /api/projects/{id}`
- `PUT /api/projects/{id}`
- `DELETE /api/projects/{id}`
- `GET /api/projects/{id}/overview`
- `GET /api/projects/{id}/journey`
- `GET /api/projects/{id}/ops`

`POST` and `PUT` accept `ProjectSpec` directly as request JSON.

Common status codes:

- Success (`GET`): `200 OK`
- Accepted (`POST`/`PUT`/`DELETE`): `202 Accepted`
- Validation errors: `400 Bad Request`
- Not found (by id): `404 Not Found`
- Enqueue/publish failure: `500 Internal Server Error` with structured recovery metadata (`op_id`, `project_id`, `next_step`, optional `project_rolled_back` on create)

### Project Journey

Endpoint:

- `GET /api/projects/{id}/journey`

Purpose:

- Returns a user-facing delivery journey snapshot for the project so UIs can render progress and suggested next steps without exposing low-level worker/event details.

Response:

```json
{
  "project": {},
  "journey": {
    "summary": "Delivery is underway: 1 of 3 environments are live.",
    "milestones": [
      {
        "id": "build",
        "title": "Build ready",
        "status": "complete | in_progress | pending | blocked | failed",
        "detail": "Latest build image: example.local/my-app:abc123."
      }
    ],
    "environments": [
      {
        "name": "dev",
        "state": "live | pending",
        "image": "example.local/my-app:abc123",
        "image_source": "latest build | deployment manifest | environment marker",
        "delivery_type": "deploy | promote | release",
        "delivery_path": "deploy/dev/rendered.yaml",
        "detail": "Deployment manifest is rendered for this environment."
      }
    ],
    "next_action": {
      "kind": "build | deploy_dev | promote | release | investigate | none",
      "label": "Deploy to dev",
      "detail": "Ship the latest build to the dev environment.",
      "environment": "dev",
      "from_env": "dev",
      "to_env": "staging"
    },
    "artifact_stats": {
      "total": 12,
      "build": 3,
      "deploy": 4,
      "promotion": 2,
      "release": 0,
      "repository": 2,
      "registration": 1,
      "other": 0
    },
    "recent_operation": {},
    "last_update_time": "2026-02-22T12:34:56Z"
  }
}
```

### Project Overview

Endpoint:

- `GET /api/projects/{id}/overview`

Purpose:

- Returns a server-owned read model for workspace delivery status with deterministic environment ordering. This payload is intended as the primary source for environment map/journey summaries in the UI.

Response:

```json
{
  "project": {},
  "overview": {
    "summary": "Delivery is underway: 1 of 3 environments are live.",
    "environments": [
      {
        "name": "dev",
        "health_status": "healthy | degraded | failing | unknown",
        "delivery_state": "live | pending",
        "running_image": "example.local/my-app:abc123",
        "delivery_type": "deploy | promote | release | none",
        "delivery_path": "deploy/dev/rendered.yaml",
        "config_readiness": "ok | unknown",
        "secrets_readiness": "unsupported",
        "last_delivery_at": "2026-02-22T12:34:56Z"
      }
    ]
  }
}
```

Notes:

- `overview.environments` ordering is deterministic (`dev` first, production last, other environments sorted between those anchors).
- `secrets_readiness` is currently `unsupported`; the platform does not expose secret-manager integration through this API.
- Read model fields avoid exposing raw environment variable maps; project spec remains available under `project`.

### Project Operation History

Endpoint:

- `GET /api/projects/{id}/ops`

Query params:

- `limit` (optional, default `20`, max `100`)
- `cursor` (optional, op id cursor returned by previous page)
- `before` (optional, RFC3339/RFC3339Nano timestamp or op id)

Purpose:

- Returns persistent operation history for the selected project so activity survives page reloads and browser session boundaries.

Response:

```json
{
  "items": [
    {
      "id": "op-id",
      "kind": "create | update | delete | ci | deploy | promote | release",
      "status": "queued | running | done | error",
      "requested": "2026-02-22T12:30:00Z",
      "finished": "2026-02-22T12:31:00Z",
      "error": "",
      "summary_message": "operation completed",
      "last_event_sequence": 14,
      "last_update_at": "2026-02-22T12:31:00Z"
    }
  ],
  "next_cursor": "op-id"
}
```

## Operations

Endpoint:

- `GET /api/ops/{opID}`
- `GET /api/ops/{opID}/events`

Response is an `Operation` object with step-level worker details. Process operations now include `delivery` metadata:

```json
{
  "kind": "deploy | promote | release",
  "delivery": {
    "stage": "deploy | promote | release",
    "environment": "dev",
    "from_env": "dev",
    "to_env": "staging"
  }
}
```

Common status codes:

- Success: `200 OK`
- Invalid id: `400 Bad Request`
- Not found: `404 Not Found`

### Operation Event Stream (SSE)

Endpoint:

- `GET /api/ops/{opID}/events`

Headers:

- `Content-Type: text/event-stream`
- `Cache-Control: no-cache`
- `Connection: keep-alive`
- `X-Accel-Buffering: no`

Behavior:

- Supports replay using `Last-Event-ID`.
- If `Last-Event-ID` is missing or outside retained history, stream begins with an `op.bootstrap` snapshot event.
- Emits heartbeat events (`op.heartbeat`) periodically to keep the stream alive.
- Runtime transport capability details are discoverable via `GET /api/system`:
  - `realtime.sse_enabled`
  - `realtime.sse_replay_window`
  - `realtime.sse_heartbeat_interval`

Event types:

- `op.bootstrap`
- `op.status`
- `step.started`
- `step.ended`
- `step.artifacts`
- `op.completed`
- `op.failed`
- `op.heartbeat`

Payload baseline fields:

- `event_id`
- `sequence`
- `op_id`
- `project_id`
- `kind`
- `status`
- `at` (RFC3339 UTC)

Payload enrichment fields (when available):

- `worker`
- `step_index`
- `total_steps`
- `progress_percent`
- `duration_ms`
- `message`
- `error`
- `artifacts` (bounded preview list)
- `delivery`:
  - `stage`
  - `environment`
  - `from_env`
  - `to_env`
- `hint`

## Artifacts

Endpoints:

- `GET /api/projects/{id}/artifacts`
- `GET /api/projects/{id}/artifacts/{path...}`

List response:

```json
{
  "files": ["relative/path.txt"]
}
```

Download response:

- Binary stream with:
  - `Content-Type: application/octet-stream`
  - `Content-Disposition: attachment; filename="<base>"`
