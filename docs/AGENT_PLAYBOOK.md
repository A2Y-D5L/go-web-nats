# Agent Playbook

Task recipes for fast, low-risk changes.

Task lookup helpers:

- `make task-list`
- `make task-show TASK=<task-id>`
- `make task-files TASK=<task-id>`
- `make task-tests TASK=<task-id>`
- `make task-audit TASK=<task-id>`

## Proposal -> Accepted -> Sprint Workflow

Use this for planning/refinement cycles.

### 1) Review proposals

1. Select `TASK=docs.proposal-lifecycle`.
2. Read all relevant proposal files in `.todos/proposals/` fully before writing output.
3. Capture overlap:
   - duplicate goals
   - conflicting scope
   - shared dependencies

### 2) Synthesize accepted proposals

1. Write accepted docs to `.todos/proposals/accepted/`.
2. Prefer the smallest number of accepted docs that still preserves clear ownership.
3. Each accepted doc should include:
   - decision summary
   - scope in/out
   - delivery milestones
   - API/domain impact
   - acceptance criteria
   - risks/mitigations

### 3) Refine into sprint tasks

1. Create/update `.todos/current-sprint/TODO.md` as the ordered execution index.
2. Create sprint task files (`TODO_Sxx_*.md`) with explicit dependency edges.
3. Every sprint task must include:
   - codebase reality check
   - why this matters
   - scope in/scope out
   - API contract changes (if any)
   - implementation instructions with concrete files/functions
   - explicit tests
   - validation commands
   - definition of done

### 4) Quality gate for AI implementation

Before finalizing sprint tasks, verify they are implementable against current code:

1. route wiring patterns (`api_types.go`, subresource dispatch in `api_projects.go`)
2. existing data models/contracts (`model.go`, `messages.go`)
3. existing worker paths (`workers_action_*.go`, `ops_bookkeeping.go`)
4. existing frontend module boundaries (`web/app_*.js`)
5. existing tests that should be extended rather than replaced

### 5) Validate docs/context changes

1. `make task-audit TASK=docs.proposal-lifecycle`
2. `make agent-check`
3. `make check`

### 6) Commit hygiene for planning artifacts

1. Treat `.todos/` as local planning context only.
2. Do not commit `.todos/` files.
3. Do not reference `.todos/` paths or `.todos` artifacts in commit messages.

## Add/Change API Endpoint

1. Update route wiring in `api_types.go` if needed.
2. Implement handler in the matching `api_*.go` file (`api_processes.go` for deploy/promotion/release events).
3. Reuse `api_runop.go` for op orchestration (do not duplicate wait/publish logic).
4. Add/adjust tests in `api_handlers_test.go` or `api_webhooks_test.go`.
5. Run `make test-api`, then `make check`.

## Add/Change Worker Behavior

1. Pick the correct worker file:
   - registration: `workers_action_registration.go`
   - bootstrap: `workers_action_bootstrap.go` + helpers
   - build: `workers_action_build.go` + `workers_action_buildkit*.go` helpers
   - deploy: `workers_action_deploy.go`
   - promotion: `workers_action_promotion.go`
2. Keep shared helpers in:
   - git operations (go-git): `workers_action_git.go`
   - webhook hook script/install + optional commit watcher: `workers_action_webhook_hooks.go`
   - file/path utilities: `workers_action_files.go`
   - build backends and mode-gated BuildKit path: `workers_action_buildkit.go`, `workers_action_buildkit_stub.go`, `workers_action_buildkit_moby.go`
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

## Change Frontend UI/UX

1. Preserve UX scope contract:
   - landing surface shows app list (or create-first CTA) only
   - app details/actions appear only in selected-app workspace context
2. Edit `web/index.html` for semantic structure and accessibility.
3. Edit `web/styles.css` using tokenized styles and responsive layout updates.
4. Edit the focused frontend JS module:
   - `web/app_core.js` for shared state/utilities/forms.
   - `web/app_render_projects_ops.js` for app-list rendering and operation views.
   - `web/app_data_artifacts.js` for API/journey/artifact data flows.
   - `web/app_render_surfaces.js` for landing/workspace visibility and top-level panel rendering.
   - `web/app_flow.js` for modal/action/monitoring and workspace lifecycle flows.
   - `web/app_events.js` for bindings/init and workspace navigation events.
   - `web/app.js` only as the bootstrap shim.
5. Keep endpoint contracts aligned with `docs/API_CONTRACTS.md`.
6. Run `make js-check`, then `make check`.

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
