SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

GO ?= go
NODE ?= node
CURL ?= curl
GIT ?= git
GOFUMPT ?= gofumpt
GOLANGCI_LINT ?= golangci-lint

APP_ADDR ?= 127.0.0.1:8080
API_BASE ?= http://$(APP_ADDR)
ARTIFACTS_ROOT ?= ./data/artifacts

TMP_DIR ?= ./.tmp
COVER_OUT ?= $(TMP_DIR)/coverage.out
SMOKE_CREATE_RESP ?= $(TMP_DIR)/smoke-create.json
GOCACHE ?= $(abspath $(TMP_DIR)/go-build-cache)
GOTMPDIR ?= $(abspath $(TMP_DIR)/go-tmp)
GOLANGCI_LINT_CACHE ?= $(abspath $(TMP_DIR)/golangci-lint-cache)

export GOCACHE
export GOTMPDIR
export GOLANGCI_LINT_CACHE

GO_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*' -not -path './data/*')

.PHONY: help \
	tools tidy \
	prepare-go-env \
	fmt fmt-check \
	vet lint lint-fix test test-race cover js-check check precommit \
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

test-race: prepare-go-env ## Run tests with race detector
	$(GO) test -race ./...

cover: prepare-go-env ## Run tests with coverage report
	@mkdir -p "$(TMP_DIR)"
	$(GO) test -coverprofile="$(COVER_OUT)" ./...
	$(GO) tool cover -func="$(COVER_OUT)"

js-check: ## Syntax-check frontend JS
	$(NODE) --check web/app.js

check: fmt-check lint vet test js-check ## Run all local quality checks

precommit: check ## Alias for check

run: ## Run API/UI server locally
	PAAS_LOCAL_API_BASE_URL="$(API_BASE)" $(GO) run .

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
