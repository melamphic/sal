.PHONY: dev build test test-integration lint migrate migrate-down generate docs docs-api docs-install tidy

# ── Local dev ──────────────────────────────────────────────────────────────────

# Start infrastructure (postgres, mailpit, minio) and run the API server.
dev:
	docker compose up -d
	go run ./cmd/api

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

# ── Code generation ────────────────────────────────────────────────────────────

# Regenerate sqlc Go code from SQL query files.
# Requires: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
generate:
	sqlc generate -f sqlc/sqlc.yaml

# Update go.mod and go.sum.
tidy:
	go mod tidy

# ── Docs ───────────────────────────────────────────────────────────────────────

# Serve the MkDocs engineering documentation site at http://localhost:8001
# Install deps first: make docs-install
docs:
	mkdocs serve --dev-addr 0.0.0.0:8001

# Open the Swagger UI in the browser (API server must be running).
docs-api:
	open http://localhost:8080/docs

# Install Python documentation dependencies.
docs-install:
	pip3 install -r requirements-docs.txt

# ── Helpers ────────────────────────────────────────────────────────────────────

# Generate a random 32-byte base64 key for ENCRYPTION_KEY.
gen-key:
	@openssl rand -base64 32

# Generate a random 64-byte hex string for JWT_SECRET.
gen-jwt-secret:
	@openssl rand -hex 64
