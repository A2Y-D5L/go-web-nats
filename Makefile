SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

GO ?= go
NODE ?= node
CURL ?= curl
GIT ?= git
DOCKER ?= docker
GOFUMPT ?= gofumpt
GOLANGCI_LINT ?= golangci-lint

APP_ADDR ?= 127.0.0.1:8080
API_BASE ?= http://$(APP_ADDR)
ARTIFACTS_ROOT ?= ./data/artifacts
LOCAL_BIN_DIR ?= $(HOME)/.local/bin
BUILDKIT_VERSION ?= v0.27.1
BUILDKIT_OS ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
BUILDKIT_ARCH_RAW ?= $(shell uname -m)
BUILDKIT_ARCH ?= $(if $(filter x86_64,$(BUILDKIT_ARCH_RAW)),amd64,$(if $(filter aarch64,$(BUILDKIT_ARCH_RAW)),arm64,$(BUILDKIT_ARCH_RAW)))
BUILDKIT_DIST ?= buildkit-$(BUILDKIT_VERSION).$(BUILDKIT_OS)-$(BUILDKIT_ARCH).tar.gz
BUILDKIT_RELEASE_URL ?= https://github.com/moby/buildkit/releases/download/$(BUILDKIT_VERSION)/$(BUILDKIT_DIST)
BUILDKIT_RUN_DIR ?= $(HOME)/.local/run/buildkit
BUILDKIT_ADDR ?= unix://$(BUILDKIT_RUN_DIR)/buildkitd.sock

TMP_DIR ?= ./.tmp
COVER_OUT ?= $(TMP_DIR)/coverage.out
SMOKE_CREATE_RESP ?= $(TMP_DIR)/smoke-create.json
GOCACHE ?= $(abspath $(TMP_DIR)/go-build-cache)
GOTMPDIR ?= $(abspath $(TMP_DIR)/go-tmp)
GOLANGCI_LINT_CACHE ?= $(abspath $(TMP_DIR)/golangci-lint-cache)
BUILDKIT_TARBALL ?= $(TMP_DIR)/$(BUILDKIT_DIST)
BUILDKIT_EXTRACT_DIR ?= $(TMP_DIR)/buildkit-$(BUILDKIT_VERSION)-$(BUILDKIT_OS)-$(BUILDKIT_ARCH)
BUILDKIT_CONTAINER_PORT ?= 1234
BUILDKIT_CONTAINER_BIND_IP ?= 127.0.0.1

export GOCACHE
export GOTMPDIR
export GOLANGCI_LINT_CACHE

GO_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*' -not -path './data/*')

.PHONY: help \
	tools tidy \
	prepare-go-env \
	fmt fmt-check \
	agent-check \
	task-list task-show task-files task-tests task-audit \
	vet lint lint-fix test test-api test-workers test-store test-model test-race cover js-check check precommit \
	setup-local setup-buildkit buildkit-install buildkit-go-deps buildkit-check buildkit-start buildkit-start-container run-buildkit run-artifact \
	run dev wait-api \
	api-list api-create api-webhook \
	smoke-registration demo-commit \
	clean clean-artifacts clean-tmp

help: ## Show available targets
	@echo "Local development targets:"
	@awk 'BEGIN {FS = ":.*## ";} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Verify local toolchain dependencies
	@set -euo pipefail; \
	for cmd in "$(GO)" "$(GIT)" "$(CURL)" "$(NODE)" "$(GOFUMPT)" "$(GOLANGCI_LINT)"; do \
		if ! command -v "$$cmd" >/dev/null 2>&1; then \
			echo "Missing required tool: $$cmd"; \
			exit 1; \
		fi; \
	done; \
	echo "Toolchain OK."; \
	"$(GO)" version; \
	"$(GIT)" --version; \
	"$(NODE)" --version

tidy: ## Sync Go module dependencies
	$(GO) mod tidy

prepare-go-env: ## Create local Go cache/temp directories
	@mkdir -p "$(TMP_DIR)" "$(GOCACHE)" "$(GOTMPDIR)" "$(GOLANGCI_LINT_CACHE)"

fmt: ## Format Go files with gofumpt
	@set -euo pipefail; \
	if [[ -z "$(strip $(GO_FILES))" ]]; then \
		echo "No Go files found."; \
		exit 0; \
	fi; \
	$(GOFUMPT) -w $(GO_FILES); \
	echo "Formatted Go files."

fmt-check: ## Fail if Go formatting is not gofumpt-clean
	@set -euo pipefail; \
	if [[ -z "$(strip $(GO_FILES))" ]]; then \
		echo "No Go files found."; \
		exit 0; \
	fi; \
	out="$$($(GOFUMPT) -l $(GO_FILES))"; \
	if [[ -n "$$out" ]]; then \
		echo "These files need gofumpt:"; \
		echo "$$out"; \
		exit 1; \
	fi; \
	echo "Go formatting clean (gofumpt)."

vet: prepare-go-env ## Run go vet
	$(GO) vet ./...

lint: prepare-go-env ## Run golangci-lint
	$(GOLANGCI_LINT) run --config .golangci.yml

lint-fix: prepare-go-env ## Run golangci-lint with auto-fix where supported
	$(GOLANGCI_LINT) run --fix --config .golangci.yml

test: prepare-go-env ## Run unit tests
	$(GO) test ./...

test-api: prepare-go-env ## Run API-focused tests
	$(GO) test ./... -run '^TestAPI_'

test-workers: prepare-go-env ## Run worker-focused tests
	$(GO) test ./... -run '^TestWorkers_'

test-store: prepare-go-env ## Run store/artifact-focused tests
	$(GO) test ./... -run '^TestStore_'

test-model: prepare-go-env ## Run model/spec-focused tests
	$(GO) test ./... -run '^TestModel_'

test-race: prepare-go-env ## Run tests with race detector
	$(GO) test -race ./...

cover: prepare-go-env ## Run tests with coverage report
	@mkdir -p "$(TMP_DIR)"
	$(GO) test -coverprofile="$(COVER_OUT)" ./...
	$(GO) tool cover -func="$(COVER_OUT)"

js-check: ## Syntax-check frontend JS
	@set -euo pipefail; \
	files="$$(rg --files web -g '*.js' | sort)"; \
	if [[ -z "$$files" ]]; then \
		echo "No frontend JS files found."; \
		exit 0; \
	fi; \
	while IFS= read -r file; do \
		$(NODE) --check "$$file"; \
	done <<< "$$files"; \
	echo "Frontend JS syntax clean."

agent-check: ## Validate agent context files and task-map references
	@set -euo pipefail; \
	required_files="AGENTS.md CODEMAP.md TASKMAP.yaml docs/AGENT_PLAYBOOK.md docs/API_CONTRACTS.md README.md scripts/taskmap.sh"; \
	for f in $$required_files; do \
		if [[ ! -f "$$f" ]]; then \
			echo "Missing required agent context file: $$f"; \
			exit 1; \
		fi; \
	done; \
	for marker in AGENTS.md CODEMAP.md TASKMAP.yaml docs/AGENT_PLAYBOOK.md docs/API_CONTRACTS.md; do \
		if ! grep -q "$$marker" README.md; then \
			echo "README.md missing agent context reference: $$marker"; \
			exit 1; \
		fi; \
	done; \
	if ! grep -q "Primary agent contract:" CODEMAP.md; then \
		echo "CODEMAP.md missing primary contract marker."; \
		exit 1; \
	fi; \
	refs="$$(grep -Eo '[A-Za-z0-9_./-]+\.(go|md|ya?ml|sh)' TASKMAP.yaml | sort -u)"; \
	for ref in $$refs; do \
		if [[ ! -e "$$ref" ]]; then \
			echo "TASKMAP.yaml references missing path: $$ref"; \
			exit 1; \
		fi; \
	done; \
	task_ids="$$(./scripts/taskmap.sh list)"; \
	for task_id in $$task_ids; do \
		./scripts/taskmap.sh files "$$task_id" >/dev/null; \
		./scripts/taskmap.sh tests "$$task_id" >/dev/null; \
	done; \
	taskmap_all_files="$$(mktemp)"; \
	taskmap_go_files="$$(mktemp)"; \
	repo_go_files="$$(mktemp)"; \
	trap 'rm -f "$$taskmap_all_files" "$$taskmap_go_files" "$$repo_go_files"' EXIT; \
	while IFS= read -r task_id; do \
		./scripts/taskmap.sh files "$$task_id" >> "$$taskmap_all_files"; \
	done < <(./scripts/taskmap.sh list); \
	: > "$$taskmap_go_files"; \
	rg '\.go$$' "$$taskmap_all_files" | sort -u > "$$taskmap_go_files" || true; \
	rg --files -g '*.go' -g '!vendor/**' -g '!data/**' -g '!.tmp/**' | sort -u > "$$repo_go_files"; \
	unmapped_non_test_go="$$(comm -23 "$$repo_go_files" "$$taskmap_go_files" | rg -v '_test\.go$$' || true)"; \
	if [[ -n "$$unmapped_non_test_go" ]]; then \
		echo "TASKMAP.yaml missing non-test Go file ownership:"; \
		echo "$$unmapped_non_test_go"; \
		exit 1; \
	fi; \
	for doc in AGENTS.md CODEMAP.md; do \
		doc_refs="$$(grep -Eo '\`[^`]+\.(go|md|ya?ml|sh)\`' "$$doc" | tr -d '\`' | sort -u || true)"; \
		for ref in $$doc_refs; do \
			if [[ "$$ref" == *'*'* ]]; then \
				continue; \
			fi; \
			if [[ ! -e "$$ref" ]]; then \
				echo "$$doc references missing path: $$ref"; \
				exit 1; \
			fi; \
		done; \
	done; \
	echo "Agent context files are consistent."

task-list: ## List task ids from TASKMAP.yaml
	@./scripts/taskmap.sh list

task-show: ## Show files/tests for a task (TASK=<task-id>)
	@set -euo pipefail; \
	if [[ -z "$(TASK)" ]]; then \
		echo "TASK is required. Example: make task-show TASK=api.webhooks"; \
		exit 1; \
	fi; \
	./scripts/taskmap.sh show "$(TASK)"

task-files: ## Show file set for a task (TASK=<task-id>)
	@set -euo pipefail; \
	if [[ -z "$(TASK)" ]]; then \
		echo "TASK is required. Example: make task-files TASK=workers.bootstrap"; \
		exit 1; \
	fi; \
	./scripts/taskmap.sh files "$(TASK)"

task-tests: ## Show test files for a task (TASK=<task-id>)
	@set -euo pipefail; \
	if [[ -z "$(TASK)" ]]; then \
		echo "TASK is required. Example: make task-tests TASK=api.registration"; \
		exit 1; \
	fi; \
	./scripts/taskmap.sh tests "$(TASK)"

task-audit: prepare-go-env ## Run scoped verification for a task (TASK=<task-id>)
	@set -euo pipefail; \
	if [[ -z "$(TASK)" ]]; then \
		echo "TASK is required. Example: make task-audit TASK=api.webhooks"; \
		exit 1; \
	fi; \
	echo "Auditing task $(TASK)..."; \
	./scripts/taskmap.sh show "$(TASK)"; \
	files="$$(./scripts/taskmap.sh files "$(TASK)" || true)"; \
	for f in $$files; do \
		if [[ ! -e "$$f" ]]; then \
			echo "Mapped file missing: $$f"; \
			exit 1; \
		fi; \
	done; \
	tests="$$(./scripts/taskmap.sh tests "$(TASK)" || true)"; \
	test_names=""; \
	for tf in $$tests; do \
		if [[ ! -f "$$tf" ]]; then \
			echo "Mapped test file missing: $$tf"; \
			exit 1; \
		fi; \
		names="$$(rg -No 'func (Test[A-Za-z0-9_]+)\(' "$$tf" | sed -E 's/.*func (Test[A-Za-z0-9_]+)\(.*/\1/' || true)"; \
		if [[ -n "$$names" ]]; then \
			test_names="$$test_names $$names"; \
		fi; \
	done; \
	if [[ -n "$$test_names" ]]; then \
		regex="^($$(echo "$$test_names" | tr ' ' '\n' | sed '/^$$/d' | sort -u | paste -sd'|' -))$$"; \
		echo "Running scoped tests: $$regex"; \
		$(GO) test ./... -run "$$regex"; \
	else \
		echo "No mapped tests for task $(TASK); skipping scoped go test."; \
	fi; \
	if [[ "$(TASK)" == "docs.context" ]]; then \
		echo "Running agent-check for docs.context..."; \
		$(MAKE) agent-check; \
	fi; \
	echo "Task audit passed for $(TASK)."

check: fmt-check agent-check lint vet test js-check ## Run all local quality checks

precommit: check ## Alias for check

setup-local: tools tidy prepare-go-env ## Prepare local toolchain/cache state

setup-buildkit: buildkit-install buildkit-go-deps ## Install BuildKit daemon + Go module deps for -tags buildkit

buildkit-install: prepare-go-env ## Install buildkitd/buildctl binaries to LOCAL_BIN_DIR
	@set -euo pipefail; \
	echo "Downloading $(BUILDKIT_RELEASE_URL)"; \
	mkdir -p "$(TMP_DIR)"; \
	rm -rf "$(BUILDKIT_EXTRACT_DIR)"; \
	$(CURL) -fsSL -o "$(BUILDKIT_TARBALL)" "$(BUILDKIT_RELEASE_URL)"; \
	mkdir -p "$(BUILDKIT_EXTRACT_DIR)" "$(LOCAL_BIN_DIR)"; \
	tar -xzf "$(BUILDKIT_TARBALL)" -C "$(BUILDKIT_EXTRACT_DIR)"; \
	buildkitd_src="$$(find "$(BUILDKIT_EXTRACT_DIR)" -type f -name buildkitd -perm -u+x | head -n1 || true)"; \
	buildctl_src="$$(find "$(BUILDKIT_EXTRACT_DIR)" -type f -name buildctl -perm -u+x | head -n1 || true)"; \
	if [[ -n "$$buildctl_src" ]]; then \
		install -m 0755 "$$buildctl_src" "$(LOCAL_BIN_DIR)/buildctl"; \
	fi; \
	if [[ -n "$$buildkitd_src" ]]; then \
		install -m 0755 "$$buildkitd_src" "$(LOCAL_BIN_DIR)/buildkitd"; \
		echo "Installed buildkitd + buildctl to $(LOCAL_BIN_DIR)"; \
	else \
		echo "buildkitd missing from release archive for $(BUILDKIT_OS)-$(BUILDKIT_ARCH)."; \
		if [[ "$(BUILDKIT_OS)" == "darwin" ]]; then \
			echo "On macOS, use a Linux BuildKit daemon (container/VM/remote) and set BUILDKIT_ADDR=tcp://127.0.0.1:$(BUILDKIT_CONTAINER_PORT)."; \
			echo "Try: make buildkit-start-container"; \
		else \
			echo "Install buildkitd from a Linux release/packaging source and place it on PATH."; \
		fi; \
	fi; \
	echo "If needed, add to PATH: export PATH=\"$(LOCAL_BIN_DIR):\$$PATH\""

buildkit-go-deps: ## Add BuildKit Go module dependencies used by -tags buildkit
	$(GO) get github.com/moby/buildkit/client@$(BUILDKIT_VERSION)
	$(GO) get github.com/moby/buildkit/frontend/dockerfile/parser@$(BUILDKIT_VERSION)
	$(GO) mod tidy

buildkit-check: ## Verify BuildKit local dependency availability
	@set -euo pipefail; \
	if ! command -v buildctl >/dev/null 2>&1; then \
		echo "buildctl not found on PATH. Try: make buildkit-install"; \
		exit 1; \
	fi; \
	if command -v buildkitd >/dev/null 2>&1; then \
		echo "buildkitd path: $$(command -v buildkitd)"; \
		buildkitd --version; \
	else \
		echo "buildkitd not found on PATH (container/remote daemon is still valid)"; \
	fi; \
	if buildctl --addr "$(BUILDKIT_ADDR)" debug workers >/dev/null 2>&1; then \
		echo "BuildKit daemon reachable at $(BUILDKIT_ADDR)"; \
	else \
		echo "BuildKit daemon not reachable at $(BUILDKIT_ADDR)"; \
		echo "Start one via: make buildkit-start"; \
		echo "or (containerized): make buildkit-start-container BUILDKIT_ADDR=tcp://$(BUILDKIT_CONTAINER_BIND_IP):$(BUILDKIT_CONTAINER_PORT)"; \
		exit 1; \
	fi

buildkit-start: ## Start buildkitd in foreground (BUILDKIT_ADDR=unix://... supported)
	@set -euo pipefail; \
	if ! command -v buildkitd >/dev/null 2>&1; then \
		echo "buildkitd not found on PATH."; \
		echo "On macOS, use: make buildkit-start-container"; \
		exit 1; \
	fi; \
	mkdir -p "$(BUILDKIT_RUN_DIR)"; \
	echo "Starting buildkitd at $(BUILDKIT_ADDR)"; \
	buildkitd --addr "$(BUILDKIT_ADDR)"

buildkit-start-container: ## Start BuildKit daemon in Docker (macOS-friendly)
	@set -euo pipefail; \
	if ! command -v $(DOCKER) >/dev/null 2>&1; then \
		echo "docker not found on PATH."; \
		exit 1; \
	fi; \
	echo "Starting containerized BuildKit daemon on tcp://$(BUILDKIT_CONTAINER_BIND_IP):$(BUILDKIT_CONTAINER_PORT)"; \
	echo "Use BUILDKIT_ADDR=tcp://$(BUILDKIT_CONTAINER_BIND_IP):$(BUILDKIT_CONTAINER_PORT) for run-buildkit/buildkit-check"; \
	$(DOCKER) run --rm --name platform-buildkitd --privileged \
		-p $(BUILDKIT_CONTAINER_BIND_IP):$(BUILDKIT_CONTAINER_PORT):1234 \
		moby/buildkit:$(BUILDKIT_VERSION) \
		--addr tcp://0.0.0.0:1234

run-buildkit: ## Run API/UI server with BuildKit support (-tags buildkit)
	PAAS_LOCAL_API_BASE_URL="$(API_BASE)" PAAS_BUILDKIT_ADDR="$(BUILDKIT_ADDR)" $(GO) run -tags buildkit ./cmd/server

run-artifact: ## Run API/UI server with artifact mode fallback
	PAAS_LOCAL_API_BASE_URL="$(API_BASE)" PAAS_IMAGE_BUILDER_MODE=artifact $(GO) run ./cmd/server

run: ## Run API/UI server locally
	PAAS_LOCAL_API_BASE_URL="$(API_BASE)" $(GO) run ./cmd/server

dev: run ## Alias for run

wait-api: ## Wait until local API is reachable
	@set -euo pipefail; \
	attempts="$${ATTEMPTS:-40}"; \
	sleep_s="$${SLEEP_SECS:-0.25}"; \
	for i in $$(seq 1 "$$attempts"); do \
		if $(CURL) -fsS "$(API_BASE)/api/projects" >/dev/null 2>&1; then \
			echo "API ready at $(API_BASE)"; \
			exit 0; \
		fi; \
		sleep "$$sleep_s"; \
	done; \
	echo "API not reachable at $(API_BASE) after $$attempts attempts."; \
	exit 1

api-list: ## List projects via API
	$(CURL) -fsS "$(API_BASE)/api/projects"

api-create: ## Create a project via registration event (PROJECT_NAME optional)
	@set -euo pipefail; \
	name="$${PROJECT_NAME:-demo-$$(date +%s)}"; \
	echo "Creating project '$$name'..."; \
	$(CURL) -fsS -X POST "$(API_BASE)/api/events/registration" \
		-H 'content-type: application/json' \
		-d "$$(printf '{"action":"create","spec":{"apiVersion":"platform.example.com/v2","kind":"App","name":"%s","runtime":"go_1.26","capabilities":["http"],"environments":{"dev":{"vars":{"LOG_LEVEL":"info"}}},"networkPolicies":{"ingress":"internal","egress":"internal"}}}' "$$name")"; \
	echo

api-webhook: ## Trigger source webhook CI (PROJECT_ID required)
	@set -euo pipefail; \
	if [[ -z "$(PROJECT_ID)" ]]; then \
		echo "PROJECT_ID is required. Example: make api-webhook PROJECT_ID=<id>"; \
		exit 1; \
	fi; \
	commit="$${WEBHOOK_COMMIT:-manual-$$(date +%s)}"; \
	$(CURL) -fsS -X POST "$(API_BASE)/api/webhooks/source" \
		-H 'content-type: application/json' \
		-d "$$(printf '{"project_id":"%s","repo":"source","branch":"main","ref":"refs/heads/main","commit":"%s"}' "$(PROJECT_ID)" "$$commit")"; \
	echo

smoke-registration: ## Smoke test create/list flow (API must be running)
	@set -euo pipefail; \
	mkdir -p "$(TMP_DIR)"; \
	name="smoke-$$(date +%s)"; \
	echo "Running registration smoke test with '$$name'..."; \
	code="$$($(CURL) -sS -o "$(SMOKE_CREATE_RESP)" -w "%{http_code}" -X POST "$(API_BASE)/api/events/registration" \
		-H 'content-type: application/json' \
		-d "$$(printf '{"action":"create","spec":{"apiVersion":"platform.example.com/v2","kind":"App","name":"%s","runtime":"go_1.26","capabilities":["http"],"environments":{"dev":{"vars":{"LOG_LEVEL":"info"}}},"networkPolicies":{"ingress":"internal","egress":"internal"}}}' "$$name")")"; \
	if [[ "$$code" != "200" ]]; then \
		echo "Create failed with status $$code"; \
		cat "$(SMOKE_CREATE_RESP)"; \
		exit 1; \
	fi; \
	$(CURL) -fsS "$(API_BASE)/api/projects" >/dev/null; \
	echo "Smoke test passed."

demo-commit: ## Commit in source repo to trigger installed local webhook (PROJECT_ID required)
	@set -euo pipefail; \
	if [[ -z "$(PROJECT_ID)" ]]; then \
		echo "PROJECT_ID is required. Example: make demo-commit PROJECT_ID=<id>"; \
		exit 1; \
	fi; \
	repo="$(ARTIFACTS_ROOT)/$(PROJECT_ID)/repos/source"; \
	if [[ ! -d "$$repo/.git" ]]; then \
		echo "Source repo not found: $$repo"; \
		exit 1; \
	fi; \
	cd "$$repo"; \
	$(GIT) checkout -B main >/dev/null 2>&1; \
	echo "// local change $$(date -u +%FT%TZ)" >> main.go; \
	$(GIT) add main.go; \
	$(GIT) commit -m "feat: local source change via make demo-commit"; \
	echo "Committed to $$repo. The hook should POST to $(API_BASE)/api/webhooks/source."

clean-artifacts: ## Remove generated local artifacts
	rm -rf "$(ARTIFACTS_ROOT)"

clean-tmp: ## Remove local temporary files
	rm -rf "$(TMP_DIR)"

clean: clean-artifacts clean-tmp ## Remove generated local files
