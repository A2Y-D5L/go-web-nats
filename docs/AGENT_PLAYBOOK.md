# Agent Playbook

Task recipes for fast, low-risk changes.

Task lookup helpers:

- `make task-list`
- `make task-show TASK=<task-id>`
- `make task-files TASK=<task-id>`
- `make task-tests TASK=<task-id>`
- `make task-audit TASK=<task-id>`

## Add/Change API Endpoint

1. Update route wiring in `api_types.go` if needed.
2. Implement handler in the matching `api_*.go` file.
3. Reuse `api_runop.go` for op orchestration (do not duplicate wait/publish logic).
4. Add/adjust tests in `api_handlers_test.go` or `api_webhooks_test.go`.
5. Run `make test-api`, then `make check`.

## Add/Change Worker Behavior

1. Pick the correct worker file:
   - registration: `workers_action_registration.go`
   - bootstrap: `workers_action_bootstrap.go` + helpers
   - build: `workers_action_build.go`
   - deploy: `workers_action_deploy.go`
2. Keep shared helpers in:
   - git operations (go-git): `workers_action_git.go`
   - webhook hook script/install + optional commit watcher: `workers_action_webhook_hooks.go`
   - file/path utilities: `workers_action_files.go`
3. Preserve op step bookkeeping calls (`markOpStepStart`/`markOpStepEnd`).
4. Run `make test-workers`, then `make check`.

Webhook-specific note:

- If webhook trigger behavior changes, include `api_types.go` when API struct fields are added/updated and keep hook + watcher dedupe semantics aligned.

## Change Pipeline Runtime

1. Edit worker startup/types in `workers_defs.go`.
2. Edit subscription/dispatch logic in `workers_loop.go`.
3. Edit result shaping in `workers_resultmsg.go`.
4. Verify subject chain constants in `config_subjects.go`.
5. Run `make test-workers`, then `make check`.

## Change Persistence/State

1. Edit KV interactions in `store.go`.
2. Keep model contract compatibility in `model.go`.
3. Preserve finalization semantics in `ops_bookkeeping.go`.
4. Run `make test-model`, then `make check`.

## Change Artifact Filesystem Behavior

1. Edit `artifacts_fs.go`.
2. Preserve path safety checks and `.git` filtering behavior.
3. Validate artifact endpoints in `api_artifacts_ops.go`.
4. Run `make test-store`, then `make check`.

## Startup/Infra Changes

1. Edit `main.go` for platform lifecycle changes.
2. Edit `cmd/server/main.go` for executable entrypoint changes.
3. Edit `logging.go` for log formatting/level/source color behavior.
4. Edit `config_runtime.go` and `config_filesystem.go` for startup defaults/path behavior.
5. Edit `infra_nats.go` for embedded NATS/JetStream boot changes.
6. Keep logging behavior consistent.
7. Run `make check`.

## Definition of Done

- `make check` is green.
- `make agent-check` is green when agent context files changed.
- No broad `nolint` additions.
- `CODEMAP.md` / `TASKMAP.yaml` updated when ownership boundaries change.

## Agent Context Updates

1. Update all impacted files together: `AGENTS.md`, `CODEMAP.md`, `TASKMAP.yaml`, `docs/AGENT_PLAYBOOK.md`.
2. Run `make task-audit TASK=docs.context` for scoped context verification.
3. If API behavior changed, update `docs/API_CONTRACTS.md`.
4. If workflow/commands changed, update `README.md`.
5. Run `make agent-check`, then `make check`.
