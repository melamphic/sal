# Salvia Backend API

This repository contains the backend API for the Salvia project. It is written in Go and utilizes a PostgreSQL database, with dependencies managed by Docker Compose.

## Prerequisites

Before you begin, ensure you have the following installed:
*   [Go](https://go.dev/doc/install) (version 1.21+ recommended)
*   [Docker](https://docs.docker.com/get-docker/) and [Docker Compose](https://docs.docker.com/compose/install/)
*   [sqlc](https://docs.sqlc.dev/en/latest/overview/install.html) for Go code generation from SQL.
*   [golangci-lint](https://golangci-lint.run/usage/install/) for linting.

## Getting Started

Follow these steps to get your local development environment up and running.

### 1. Configuration

The application requires a set of environment variables to run. A template is provided in `.env.example`.

1.  **Create an `.env` file:**
    ```bash
    cp .env.example .env
    ```

2.  **Generate Secrets:** The `.env` file requires a `JWT_SECRET` and an `ENCRYPTION_KEY`. You can generate secure values for these using the provided `make` commands:
    ```bash
    make gen-jwt-secret
    make gen-key
    ```
    Copy the output from these commands and paste them into the corresponding fields in your `.env` file.

3.  **Review Other Variables:** The default values in the `.env` file are configured to work with the local `docker-compose` setup. You should not need to change them for local development.

### 2. Start Infrastructure

The project uses Docker Compose to manage its dependencies (Postgres, Mailpit for email testing, and Minio for S3-compatible storage).

Start all services in the background:
```bash
make infra
```
This is equivalent to running `docker compose up -d`.

### 3. Run Database Migrations

Once the Postgres container is running, apply the database schema:
```bash
make migrate
```

### 4. Run the Application

You can now start the API server:
```bash
make dev
```
This command will first ensure the infrastructure is running and then start the Go application. The API server will be available at `http://localhost:8080`.

Alternatively, you can run the Go application directly:
```bash
go run ./cmd/api
```

## Usage

### API Documentation

The API is documented using OpenAPI (Swagger). Once the server is running, you can access the interactive Swagger UI at:
*   **http://localhost:8080/docs**

### Local Services

The `docker-compose` setup exposes the following services:

*   **PostgreSQL:** `localhost:5432`
*   **Mailpit (Email Viewer):** http://localhost:8025
    *   All emails sent by the application in the `dev` environment will be captured here.
*   **Minio (S3 Storage):** http://localhost:9001
    *   Console for viewing stored files. The S3-compatible API is at `http://localhost:9000`.

## Development

The `Makefile` contains several useful commands for development:

*   `make dev`: Start all services and the API.
*   `make build`: Build a production binary of the API.
*   `make test`: Run unit tests.
*   `make test-integration`: Run all tests, including integration tests (requires Docker).
*   `make lint`: Run the linter.
*   `make fmt`: Format all Go files.
*   `make generate`: Regenerate Go code from SQL using `sqlc`.
*   `make migrate`: Apply database migrations.
*   `make migrate-down`: Roll back the last migration.
*   `make infra`: Start Docker dependencies only.
*   `make infra-down`: Stop Docker dependencies.
