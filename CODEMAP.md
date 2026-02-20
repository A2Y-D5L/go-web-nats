# Code Map

This repository is intentionally organized so an LLM can load only the files needed for a task.

## Read Order (Fastest Context Build)

1. `main.go`
2. `config.go`
3. `model.go`
4. `messages.go`
5. `workers_defs.go`
6. `workers_loop.go`
7. `api_types.go`
8. `api_runop.go`
9. `store.go`
10. `artifacts_fs.go`

## File Responsibilities

- `main.go`: process bootstrap, structured/color logger, worker startup, HTTP server startup.
- `ui_embed.go`: embedded static web assets.
- `config.go`: global constants and runtime defaults.
- `model.go`: domain types (`Project`, `Operation`) and spec validation/normalization.
- `messages.go`: NATS worker message schemas.
- `infra_nats.go`: embedded NATS + JetStream bootstrap.
- `store.go`: KV-backed persistence API for projects and operations.
- `artifacts_fs.go`: filesystem artifact store implementation.
- `waiters.go`: in-memory operation waiter hub for request/response synchronization.
- `workers_defs.go`: worker interface/types and constructor wiring.
- `workers_loop.go`: worker subscription loop and message execution flow.
- `workers_resultmsg.go`: worker result shaping/publish helpers.
- `workers_action_registration.go`: registration worker + registration artifact writes.
- `workers_action_git.go`: local git command helpers and repo initialization.
- `workers_action_files.go`: shared file upsert/missing-path helpers and sorted-path utilities.
- `workers_action_webhook_hooks.go`: local API endpoint discovery and git hook script install/rendering.
- `workers_action_bootstrap.go`: repo bootstrap worker orchestrator.
- `workers_action_bootstrap_helpers.go`: repo bootstrap helper stages (seed/commit/webhook metadata).
- `workers_action_build.go`: image builder worker.
- `workers_action_deploy.go`: manifest renderer/deployer worker.
- `workers_render.go`: shared rendering and naming helpers.
- `ops_bookkeeping.go`: operation step tracking and finalization helpers.
- `api_types.go`: API container type, route wiring, request logging middleware, event payload types.
- `api_registration.go`: registration endpoint handlers and helper flows.
- `api_webhooks.go`: source webhook endpoint and branch filtering.
- `api_projects.go`: project CRUD handlers.
- `api_artifacts_ops.go`: artifact and op read endpoints.
- `api_runop.go`: op orchestration path (publish, wait, finalize).
- `nats_subscriptions.go`: final worker result subscription for waking API waiters.
- `utils.go`: small shared helpers (`newID`, JSON write utilities).

## Task-Oriented Entry Points

- Add/modify API endpoint: start in `api_types.go`, then the matching `api_*.go` file.
- Change pipeline behavior: start in `workers_action_*.go`.
- Change worker pub/sub flow: `workers_defs.go`, `workers_loop.go`, `workers_resultmsg.go`, and `messages.go`.
- Change persistence behavior: `store.go`.
- Change local artifact layout: `artifacts_fs.go`.
- Change defaults/constants: `config.go`.

## Agent Rules of Thumb

- Prefer editing one task file at a time (`api_*` or `workers_action_*`), then run `make check`.
- Avoid editing `main.go` unless changing startup/bootstrap/logging behavior.
- Treat `model.go` and `messages.go` as contracts; update call sites in the same change.
- Ignore runtime-generated state under `data/` and `.tmp/` when reading context.
