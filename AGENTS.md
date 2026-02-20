# AGENTS

Operational runbook for coding agents working in this repository.

## Objective

- Maintain a local-first demo platform with real behavior (embedded NATS, real local git repos, local webhook loopback, optional in-process commit watcher).
- Optimize for safe, incremental, reviewable edits with strong static checks.

## Start Here (Every Task)

1. Read `CODEMAP.md`.
2. Read `TASKMAP.yaml` and pick the closest `tasks[].id`.
   - Optional helpers:
     - `make task-list`
     - `make task-show TASK=<id>`
     - `make task-files TASK=<id>`
     - `make task-tests TASK=<id>`
     - `make task-audit TASK=<id>`
3. Read `docs/API_CONTRACTS.md` when touching any `api_*.go` endpoint behavior.
4. Edit only files listed for that task unless a boundary change is required.
5. Run `make check` before finishing.

## Hard Constraints

- Do not relax lint policy in `.golangci.yml`.
- Do not replace real flows with mocks in production code paths.
- Do not add broad `//nolint` directives.
- Do not introduce destructive behavior in local repos/artifacts by default.
- Keep compatibility with schema defaults/validation in `model.go`.

## System Invariants

- Two API entry pathways must work:
  - registration events: `POST /api/events/registration`
  - source webhook events: `POST /api/webhooks/source`
- Source-repo CI trigger dedupe must prevent duplicate CI for the same commit when webhook hooks and watcher both run.
- Worker chain order and subjects must remain coherent:
  - `registrar` -> `repoBootstrap` -> `imageBuilder` -> `manifestRenderer`
- CI (`OpCI`) starts at build stage, not at registration stage.
- Delete flow must clean project artifacts and finalize operation state.

## File Ownership Rules

- API routing/types/middleware: `api_types.go`
- Registration API behavior: `api_registration.go`
- Webhook API behavior: `api_webhooks.go`
- Deployment/promotion API behavior: `api_processes.go`
- Projects CRUD API behavior: `api_projects.go`
- Artifacts and ops read endpoints: `api_artifacts_ops.go`
- Op orchestration/wait/publish: `api_runop.go`
- Worker runtime loop: `workers_defs.go`, `workers_loop.go`, `workers_resultmsg.go`
- Worker business logic: `workers_action_*.go`
- Promotion worker logic: `workers_action_promotion.go`
- Rendering helpers: `workers_render.go`
- Persistence: `store.go`
- Op step/finalize bookkeeping: `ops_bookkeeping.go`
- Bootstrapping/process lifecycle: `main.go` and `cmd/server/main.go`
- Logging behavior and color/source formatting: `logging.go`
- Runtime defaults/timeouts: `config_runtime.go`
- Subject/KV names: `config_subjects.go`
- Domain defaults/phases: `config_domain.go`
- File mode/path constants: `config_filesystem.go`

## Edit Strategy

- Prefer small, local edits over cross-cutting refactors.
- Keep each file focused on one concern.
- If adding a new behavior, add/extend the nearest focused file rather than creating a new monolith.
- If a file trends too large (roughly >250 lines), split by responsibility.

## Validation Commands

- Full gate (required): `make check`
- Fast loop:
  - `make task-files TASK=<id>`
  - `make task-tests TASK=<id>`
  - `make task-audit TASK=<id>`
  - `make test-api` / `make test-workers` / `make test-store` / `make test-model`
  - `make lint`
  - `go test ./...`

## Context Sync Rule

- When moving/renaming/splitting files, update `CODEMAP.md`, `TASKMAP.yaml`, and `docs/AGENT_PLAYBOOK.md` in the same change.
- Run `make agent-check` immediately after doc/context updates, then run `make check`.

## Testing Expectations

- Add or update tests for behavior changes.
- Preserve existing local integration-style tests (git + filesystem behavior).
- Avoid adding flaky timing assumptions; use bounded waits/timeouts.

## Security and Safety

- Keep artifact path traversal protections intact.
- Keep webhook branch filtering strict (`main` only).
- Preserve explicit file modes and controlled write locations.

## Review Output Format (for agent responses)

- What changed
- Why it changed
- Validation run and result
- Remaining risks (if any)
