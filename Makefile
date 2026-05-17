.PHONY: dev restart build test test-integration lint migrate migrate-down migrate-status docs docs-api docs-install tidy smoke-billing validate-content
SHELL := /bin/bash
# ── Local dev ──────────────────────────────────────────────────────────────────

# Start infrastructure (postgres, mailpit, minio) and run the API server.
dev:
	docker compose up -d
	go run ./cmd/api

# Start infrastructure and run the API server with hot reload (requires air).
watch:
	docker compose up -d
	@command -v air > /dev/null || (echo "air not found — installing..." && go install github.com/air-verse/air@latest)
	$$(go env GOPATH)/bin/air

# Kill any running API server on :8080 then start dev again. Useful when a
# previous `make dev` was interrupted without releasing the port (or when
# restarting after a code change since `go run` doesn't auto-reload).
# The leading dash on the kill line tells make to ignore a non-zero exit
# (e.g. nothing listening) and continue.

restart:
	-@lsof -ti :8080 | xargs kill -9 2>/dev/null || true
	$(MAKE) dev

# Start infrastructure only (no API server).
infra:
	docker compose up -d

# Stop infrastructure.
infra-down:
	docker compose down

# ── Build ──────────────────────────────────────────────────────────────────────

build:
	go build -o bin/api ./cmd/api

# ── Tests ──────────────────────────────────────────────────────────────────────

# Unit tests only — fast, no Docker required.
test:
	go test -count=1 -race ./...

# All tests including integration tests — requires Docker.
test-integration:
	go test -count=1 -race -tags=integration ./...

# ── Content validation ─────────────────────────────────────────────────────────

# Validate all embedded form and policy YAML files for structural correctness.
validate-content:
	go run ./cmd/validate-content

# ── Code quality ───────────────────────────────────────────────────────────────

GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null || go env GOPATH | xargs -I{} echo {}/bin/golangci-lint)

lint:
	$(GOLANGCI_LINT) run ./...

# Format all Go files.
fmt:
	gofmt -w .
	go run golang.org/x/tools/cmd/goimports@latest -w .

# ── Database ───────────────────────────────────────────────────────────────────

# Load DATABASE_URL from .env if present, then run goose.
# Usage: make migrate / migrate-down / migrate-status
_GOOSE = go run github.com/pressly/goose/v3/cmd/goose@v3.24.2 -dir migrations postgres

migrate:
	@export $$(grep -v '^#' .env | xargs) 2>/dev/null; $(_GOOSE) "$$DATABASE_URL" up

migrate-down:
	@export $$(grep -v '^#' .env | xargs) 2>/dev/null; $(_GOOSE) "$$DATABASE_URL" down

migrate-status:
	@export $$(grep -v '^#' .env | xargs) 2>/dev/null; $(_GOOSE) "$$DATABASE_URL" status

# ── Helpers ────────────────────────────────────────────────────────────────────

# Update go.mod and go.sum.
tidy:
	go mod tidy

# ── Docs ───────────────────────────────────────────────────────────────────────

# Resolve python3 — prefer homebrew python (has pip access) over Xcode's python.
PYTHON3 := $(shell command -v /opt/homebrew/bin/python3.13 2>/dev/null || command -v python3 2>/dev/null)

# Serve the MkDocs engineering documentation site at http://localhost:8001
docs:
	@$(PYTHON3) -c "import mkdocs" 2>/dev/null || (echo "mkdocs not found — running: make docs-install" && $(MAKE) docs-install)
	$(PYTHON3) -m mkdocs serve --dev-addr 0.0.0.0:8001

# Open the Swagger UI in the browser (API server must be running).
docs-api:
	open http://localhost:8080/docs

# Install Python documentation dependencies.
docs-install:
	$(PYTHON3) -m pip install --break-system-packages -r requirements-docs.txt

# ── Smoke tests ────────────────────────────────────────────────────────────────

# Exercise the billing-enforcement bundle (#134/135/137 fully, #136 partial)
# end-to-end against a running `make dev` instance. Provisions a throwaway
# clinic, asserts behaviour, cleans up. Pass KEEP=1 to skip cleanup so you
# can inspect the seeded rows after a run.
smoke-billing:
	@if [ "$$KEEP" = "1" ]; then go run ./cmd/billing-smoke -keep; else go run ./cmd/billing-smoke; fi

# ── Helpers ────────────────────────────────────────────────────────────────────

# Generate a random 32-byte base64 key for ENCRYPTION_KEY.
gen-key:
	@openssl rand -base64 32

# Generate a random 64-byte hex string for JWT_SECRET.
gen-jwt-secret:
	@openssl rand -hex 64
