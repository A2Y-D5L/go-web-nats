# Code Map

This repository is intentionally organized so an LLM can load only the files needed for a task.

Primary agent contract: `AGENTS.md`

## Read Order (Fastest Context Build)

1. `main.go`
2. `cmd/server/main.go`
3. `logging.go`
4. `config.go`
5. `model.go`
6. `messages.go`
7. `workers_defs.go`
8. `workers_loop.go`
9. `api_types.go`
10. `api_runop.go`
11. `store.go`
12. `artifacts_fs.go`

## File Responsibilities

- `main.go`: platform runtime bootstrap (`Run`) and lifecycle wiring.
- `logging.go`: structured/color logger and source/level formatting.
- `cmd/server/main.go`: executable entrypoint.
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
- `scripts/taskmap.sh`: task lookup helper for reading `TASKMAP.yaml` quickly.

## Test Files

- `waiters_test.go`: waiter hub concurrency and delivery behavior.
- `api_handlers_test.go`: project/artifact handler routing behavior.
- `api_webhooks_test.go`: webhook branch filter behavior.
- `workers_messages_test.go`: worker/result message compatibility.
- `model_spec_test.go`: spec normalization/validation/rendering behavior.
- `workers_git_test.go`: git bootstrap + hook script behavior.
- `artifacts_fs_test.go`: filesystem artifact listing safety behavior.

## Task-Oriented Entry Points

- Add/modify API endpoint: start in `api_types.go`, then the matching `api_*.go` file.
- Change pipeline behavior: start in `workers_action_*.go`.
- Change worker pub/sub flow: `workers_defs.go`, `workers_loop.go`, `workers_resultmsg.go`, and `messages.go`.
- Change persistence behavior: `store.go`.
- Change local artifact layout: `artifacts_fs.go`.
- Change defaults/constants: `config.go`.

## Agent Rules of Thumb

- Prefer editing one task file at a time (`api_*` or `workers_action_*`), then run `make check`.
- Avoid editing `main.go` or `cmd/server/main.go` unless changing startup/bootstrap/logging behavior.
- Treat `model.go` and `messages.go` as contracts; update call sites in the same change.
- Ignore runtime-generated state under `data/` and `.tmp/` when reading context.
- Prefer fast scoped checks first: `make test-api`, `make test-workers`, `make test-store`, `make test-model`.
- Use task lookup helpers to scope context quickly: `make task-list`, `make task-show TASK=<id>`.
