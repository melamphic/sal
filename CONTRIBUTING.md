# Contributing to sal

Thank you for contributing to the Salvia backend. This document explains how to work on the codebase, submit changes, and get them merged.

---

## Table of Contents

- [Ground Rules](#ground-rules)
- [Setting Up Your Environment](#setting-up-your-environment)
- [Workflow](#workflow)
- [Branching](#branching)
- [Commit Messages](#commit-messages)
- [Pull Requests](#pull-requests)
- [Code Standards](#code-standards)
- [Testing Requirements](#testing-requirements)
- [Adding a New Module](#adding-a-new-module)
- [Adding a Migration](#adding-a-migration)
- [Documentation](#documentation)
- [Getting Help](#getting-help)

---

## Ground Rules

Before writing a single line of code, read [CLAUDE.md](CLAUDE.md) in full. It contains the non-negotiable rules for this repo — layer separation, error wrapping format, security requirements, test naming, and more. These rules are enforced by CI and code review.

Key rules to internalise:

- SQL belongs in **repository only** — never in service or handler
- Business logic belongs in **service only** — never in handler or repository
- HTTP parsing belongs in **handler only** — never in service
- Every external dependency is behind an **interface**
- Every error is **wrapped** with `fmt.Errorf("module.layer.func: %w", err)`
- PII is **encrypted** before it reaches the DB — no exceptions
- Every route that touches data **declares its permission** — no implicit access

---

## Setting Up Your Environment

```bash
# 1. Fork and clone
git clone https://github.com/<your-fork>/sal.git && cd sal

# 2. Install Go tools
go install golang.org/x/tools/cmd/goimports@latest
brew install golangci-lint  # or see https://golangci-lint.run/usage/install/

# 3. Set up local environment
cp .env.example .env
make gen-key          # paste output into ENCRYPTION_KEY in .env
make gen-jwt-secret   # paste output into JWT_SECRET in .env

# 4. Start local services
make infra

# 5. Run migrations
make migrate

# 6. Verify everything works
make test
make lint
```

---

## Workflow

1. **Pick a task** — work from an open GitHub issue or a phase in `BACKEND_PLAN.md`. If you're building something not tracked, open an issue first.
2. **Branch** — create a branch from `main` following the [naming convention](#branching).
3. **Build** — write the code, keeping to the layer rules and CLAUDE.md.
4. **Test** — every service method needs a unit test; every repository method needs an integration test. Tests must pass before you open a PR.
5. **Lint** — `make lint` must pass with zero errors.
6. **PR** — open a pull request with a clear description. Link the relevant issue or phase.
7. **Review** — address feedback. One approving review is required to merge.
8. **Merge** — squash merge into `main`.

---

## Branching

```
feat/<phase>-<short-description>     new feature
fix/<short-description>              bug fix
chore/<short-description>            tooling, deps, config
docs/<short-description>             documentation only
refactor/<short-description>         code restructure, no behaviour change
```

Examples:

```
feat/phase-1-audio-ingestion
fix/magic-link-replay-race
chore/update-pgx-v5
docs/auth-flow-diagram
```

Branches are created from and merged back into `main`. Long-running feature branches should rebase on `main` regularly.

---

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>

[optional body — explain WHY, not WHAT]

[optional footer — BREAKING CHANGE or issue refs]
```

Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`

Scope: the module or package affected (`auth`, `clinic`, `staff`, `crypto`, `migrations`)

Examples:

```
feat(auth): add magic link token expiry check

fix(clinic): return ErrConflict on duplicate email_hash from DB

chore(deps): update pgx to v5.6.0

test(staff): add integration tests for UpdatePermissions

docs(auth): document refresh token rotation flow
```

Keep the summary under 72 characters. Use the body to explain decisions, not mechanics. The diff explains what changed — the message explains why.

---

## Pull Requests

### Before opening a PR

```bash
make test             # all unit tests pass
make test-integration # all integration tests pass (requires Docker)
make lint             # zero lint errors
```

### PR description template

```markdown
## What

Short description of what this PR does.

## Why

Why this change is needed — link to issue or phase.

## How

Any non-obvious implementation decisions worth explaining.

## Testing

How the change was tested — unit tests, integration tests, manual steps.

## Checklist

- [ ] Unit tests added/updated
- [ ] Integration tests added/updated (if repo layer changed)
- [ ] Migration added with `-- +goose Down` (if schema changed)
- [ ] No PII logged or stored unencrypted
- [ ] Permissions declared on every new route
- [ ] `make lint` passes
- [ ] `make test` passes
- [ ] `make test-integration` passes
```

### Review SLA

Reviews are aimed to complete within 1–2 business days. If a PR has no review after 3 days, ping in the team channel.

---

## Code Standards

### File naming

Four files per domain package, no exceptions:

```
handler.go    repository.go    service.go    routes.go
```

### Interfaces

Define the `repo` interface in `internal/<module>/repo.go`. The interface lives in the package that **uses** it, not the package that implements it.

### Error wrapping

```go
// Always — context tells you exactly where to look
return nil, fmt.Errorf("auth.service.SendMagicLink: %w", err)

// Never — raw errors, no context
return nil, err

// Never — swallowed errors
result, _ := repo.Get(ctx, id)
```

### Struct initialisation

All struct fields must be explicitly initialised in constructors (`exhaustruct` lint rule). This prevents silent zero-value bugs when a new dependency is added.

```go
// Correct
return &Service{
    repo:   repo,
    cipher: cipher,
    mailer: mailer,
    cfg:    cfg,
}

// Wrong — new fields silently zero-valued
return &Service{repo: repo}
```

### No globals

Dependencies flow through constructors. No package-level `var db *pgxpool.Pool` or similar.

### Comments

- Exported symbols have a Go doc comment (`// FunctionName does ...`)
- Non-obvious logic has a comment explaining **why**, not what
- No `// TODO` without a linked GitHub issue: `// TODO(#123): ...`
- No commented-out code in PRs

---

## Testing Requirements

Every PR that touches logic must include tests.

### Unit tests (required for all service changes)

- Lives in `<package>/<file>_test.go` alongside the code it tests
- Uses an in-memory `fakeRepo` (see `internal/clinic/fake_repo_test.go`)
- No Docker, no network — runs in milliseconds
- `t.Parallel()` on every test that doesn't share state
- Naming: `Test{Type}_{Method}_{Scenario}`

```go
func TestService_Register_DuplicateEmail_ReturnsConflict(t *testing.T) {
    t.Parallel()
    // ...
}
```

### Integration tests (required for all repository changes)

- Build tag: `//go:build integration`
- Uses `testutil.IntegrationMain(m)` in `TestMain`
- Gets a real `*pgxpool.Pool` via `testutil.NewTestDB(t)` (tables truncated before each test)
- Covers the happy path + the main error cases (not found, constraint violations)

```go
//go:build integration

func TestMain(m *testing.M) { testutil.IntegrationMain(m) }

func TestRepository_Create_DuplicateSlug_ReturnsConflict(t *testing.T) {
    t.Parallel()
    pool := testutil.NewTestDB(t)
    r := mymodule.NewRepository(pool)
    // ...
}
```

---

## Adding a New Module

1. **Create the package:**
   ```bash
   mkdir internal/<module>
   touch internal/<module>/{handler,service,repository,routes,repo}.go
   ```

2. **Define the `repo` interface** in `internal/<module>/repo.go`

3. **Add the migration:**
   ```
   migrations/<next-sequence>_create_<module>.sql
   ```

4. **Wire in `internal/app/app.go`:**
   ```go
   myRepo := mymodule.NewRepository(db)
   mySvc  := mymodule.NewService(myRepo, cipher)
   myH    := mymodule.NewHandler(mySvc)
   myH.Mount(r)
   ```

5. **Write unit tests** with a `fakeRepo` in `internal/<module>/fake_repo_test.go`

6. **Write integration tests** in `internal/<module>/repository_integration_test.go`

7. **Document the module** in `docs/<module>.md` and add it to `mkdocs.yml`

---

## Adding a Migration

```bash
# Name the file with the next sequence number
touch migrations/00005_create_subjects.sql
```

Every migration requires both directions:

```sql
-- +goose Up
CREATE TABLE subjects (
    id          UUID PRIMARY KEY,
    clinic_id   UUID NOT NULL REFERENCES clinics(id),
    -- ...
);

-- +goose Down
DROP TABLE subjects;
```

Rules:

- **Never modify a committed migration.** Always add a new file.
- Every new table on a tenant entity has `clinic_id UUID NOT NULL REFERENCES clinics(id)`
- PII columns are documented with `-- PII: encrypted`
- String enums use `VARCHAR CHECK (col IN (...))` — never PostgreSQL `ENUM`

---

## Documentation

Engineering docs live in `docs/` and are built with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/).

```bash
make docs-install   # first time only
make docs           # serve at http://localhost:8001
```

If your PR adds a new module, endpoint, or significant behaviour: **update or add the relevant doc page and add it to `mkdocs.yml`**. Documentation is not optional.

---

## Getting Help

- **Architecture questions** — read `docs/architecture.md` and `CLAUDE.md` first
- **Compliance questions** — read `docs/compliance.md`
- **Something isn't clear** — open a GitHub Discussion or ask in the team channel
- **Found a bug** — open a GitHub issue with reproduction steps
- **Security issue** — do not open a public issue; contact the team lead directly

---

## Code of Conduct

Be direct, be professional, and be kind. We review code, not people. Feedback should be specific and constructive. If something isn't clear, ask — don't assume intent.
