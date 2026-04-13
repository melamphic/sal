.PHONY: dev build test test-integration lint migrate migrate-down generate docs tidy

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

lint:
	golangci-lint run ./...

# Format all Go files.
fmt:
	gofmt -w .
	goimports -w .

# ── Database ───────────────────────────────────────────────────────────────────

# Run all pending migrations.
migrate:
	go run ./cmd/api migrate up

# Roll back the most recent migration.
migrate-down:
	go run ./cmd/api migrate down

# Print migration status.
migrate-status:
	go run ./cmd/api migrate status

# ── Code generation ────────────────────────────────────────────────────────────

# Regenerate sqlc Go code from SQL query files.
# Requires: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
generate:
	sqlc generate -f sqlc/sqlc.yaml

# Update go.mod and go.sum.
tidy:
	go mod tidy

# ── Docs ───────────────────────────────────────────────────────────────────────

# Open Swagger UI in the browser (API server must be running).
docs:
	open http://localhost:8080/docs

# ── Helpers ────────────────────────────────────────────────────────────────────

# Generate a random 32-byte base64 key for ENCRYPTION_KEY.
gen-key:
	@openssl rand -base64 32

# Generate a random 64-byte hex string for JWT_SECRET.
gen-jwt-secret:
	@openssl rand -hex 64
