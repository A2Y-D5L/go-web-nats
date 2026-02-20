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
    "name": "demo-app",
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

Success (`create` / `update`) response:

- Status: `200 OK`

```json
{
  "project": {},
  "op": {},
  "final": {}
}
```

Success (`delete`) response:

- Status: `200 OK`

```json
{
  "deleted": true,
  "op": {},
  "final": {}
}
```

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
- Only `dev` is accepted by deployment events; higher environments must use promotion events.

Success response:

- Status: `200 OK`

```json
{
  "project": {},
  "op": {},
  "final": {}
}
```

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
- Both environments must be defined for the project (except `dev`, which is always supported for deployment/promotion state).

Success response:

- Status: `200 OK`

```json
{
  "project": {},
  "op": {},
  "final": {}
}
```

## Projects

Endpoints:

- `GET /api/projects`
- `POST /api/projects`
- `GET /api/projects/{id}`
- `PUT /api/projects/{id}`
- `DELETE /api/projects/{id}`

`POST` and `PUT` accept `ProjectSpec` directly as request JSON.

Common status codes:

- Success: `200 OK`
- Validation errors: `400 Bad Request`
- Not found (by id): `404 Not Found`

## Operations

Endpoint:

- `GET /api/ops/{opID}`

Response is an `Operation` object with step-level worker details.

Common status codes:

- Success: `200 OK`
- Invalid id: `400 Bad Request`
- Not found: `404 Not Found`

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
