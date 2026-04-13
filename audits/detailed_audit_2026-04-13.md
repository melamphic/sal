# Detailed Audit Report: April 13, 2026

This report provides a systematic, file-by-file audit of the Salvia repository, evaluating architectural compliance, security, and technical debt.

---

## 1. Global Project State & Infrastructure

**Root Configuration & Tooling:**
- **`go.mod` & `go.sum`:** Dependencies are up to date (Go 1.23), but there is a divergence between tracked and untracked files causing build instability.
- **`Makefile`:** Well-defined, includes `gen-key` and `gen-jwt-secret` helpers which encourages secure secret rotation.
- **`docker-compose.yml`:** Correctly sets up PostgreSQL 17, Mailpit (SMTP), and MinIO (S3).
- **`CLAUDE.md`:** Contains strict engineering rules. These served as the primary benchmark for this audit.

**Database Migrations (`migrations/`):**
- **Architecture:** Uses `goose` with embedded migrations.
- **Audit:**
  - `00001` through `00009` are tracked and follow the `Up/Down` pattern.
  - `00010_create_notes.sql` is **untracked** but exists on disk, creating a migration gap for other developers.
  - `00003_create_auth_tokens.sql`: Correctly hashes tokens (SHA-256) and encrypts IP addresses, meeting SOC 2/HIPAA PII requirements.
  - **Multi-tenancy:** `form_groups`, `forms`, and `clinic_form_style_versions` correctly include `clinic_id`.

---

## 2. Layer & Architectural Audit

Package structures generally follow the mandated 4-file pattern (`handler.go`, `service.go`, `repository.go`, `routes.go`), but several violations of the "Layer Rules" were identified:

#### **Violation: Forbidden Imports & Logic Leakage**
- **`internal/audio/service.go`**: Imports `github.com/jackc/pgx/v5`. **Forbidden.** Services must not import SQL drivers.
- **`internal/audio/service.go`**: The `NewService` constructor takes `*river.Client[pgx.Tx]`. This leaks database transaction types into the service layer.
- **`internal/auth/repository.go`**: Uses `time.Now()` directly. **Violation.** `CLAUDE.md` mandates `domain.TimeNow()` for testability.

#### **Violation: Error Handling (Wrapping)**
- **`internal/patient/handler.go`**: Found `return nil, errors.New(...)` (naked error creation).
- **`internal/app/app.go`**: Found `return nil, err`. **Violation.** `CLAUDE.md` mandates wrapping: `fmt.Errorf("module.layer.func: %w", err)`.

---

## 3. Module Deep-Dive

#### **`internal/auth` (Grade: A-)**
- **Strengths:** Passwordless magic link flow is modern and secure. SHA-256 hashing of tokens is correctly implemented.
- **Weakness:** `repository.go` uses `time.Now()` in `used_at` checks, which will make temporal testing difficult.

#### **`internal/patient` (Grade: B+)**
- **Strengths:** PII encryption logic for contacts and subjects is robust. Multi-tenancy is strictly enforced.
- **Fix Verified:** The `ListSubjects` permission bypass identified in the April 7 audit is resolved.
- **Weakness:** Complex `vet_details` mapping in `handler.go` contains some unwrapped errors.

#### **`internal/audio` (Grade: B)**
- **Strengths:** Uses River for background transcription. Clean S3 upload flow (pre-signed URLs).
- **Critical Issue:** `jobs.go` defines a `RecordingProvider` interface where the comment claims `GetTranscript` returns two values, but the actual implementation only returns one. This will cause a panic or compilation error when wired.

#### **`internal/forms` (Grade: A)**
- **Strengths:** Architecturally sound. Semver logic for `nextVersion` is clean. The "Rollback" feature is implemented as a new draft, ensuring a strong audit trail.
- **Weakness:** Policy check is a stub.

#### **`internal/notes` (Grade: C)**
- **Status:** WIP and untracked.
- **Critical Issue:** The `ExtractNoteWorker` hardcodes `overallPrompt` to `""`. This is a "silent" logic bug where the AI loses the form's context, leading to poor extraction quality.

#### **`internal/extraction` (Grade: B-)**
- **Status:** WIP and untracked.
- **Issues:** 
  - `gemini.go`: Reference to `gemini-2.5-flash` is a typo (likely intended to be `2.0` or `1.5`).
  - `openai.go`: Empty placeholder. The factory returns `nil` instead of an error, which could cause a nil-pointer dereference in the caller if not handled.

---

## 4. Security & Multi-tenancy

- **PII Protection:** The project correctly uses AES-256-GCM for PII/PHI (patient names, phone numbers, IPs). 
- **Multi-tenancy:** `internal/patient/repository.go` and `internal/forms/repository.go` were verified. Every query includes `WHERE clinic_id = $1`.
- **Audit Logging:** The system uses `created_by` and `updated_by` UUIDs consistently across tables.

---

## 5. Final Audit Summary

| Component | Status | Priority |
|---|---|---|
| **Compilation** | **Broken** | **CRITICAL** (Unused imports in `app.go`) |
| **Git Hygiene** | **Poor** | **HIGH** (Untracked core modules) |
| **Arch. Compliance** | **Fair** | **MEDIUM** (`pgx` in services, `time.Now` violations) |
| **Core Features** | **Strong** | **LOW** (Form/Audio logic is well-written) |

**Overall Recommendation:** A "stabilization" phase is required to commit untracked files, fix the `app.go` wiring, and reconcile the `RecordingProvider` signature mismatch before further feature development.
