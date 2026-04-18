# Salvia Backend - Deep Audit & Risk Analysis

This document provides a comprehensive technical audit of the Salvia backend, focusing on concurrency, data integrity, security, and architectural scalability.

---

## 1. Concurrency & Background Jobs (River)

### [ISSUE] Atomicity Risk in AI Extraction
**Location:** `internal/notes/jobs.go` (`Work` method)
**Description:** The extraction worker updates note fields in bulk via `UpsertNoteFields` and then separately updates the note status to `draft` via `UpdateNoteStatus`.
**Risk:** If the worker crashes *between* the field upsert and the status update, the note will remain in `extracting` status indefinitely while the database contains the AI results. The system has no "zombie job" reaper for note statuses.
**Fix:** Perform the field upsert and status transition inside a single database transaction within the repository.

### [ISSUE] Inefficient Report Generation
**Location:** `internal/reports/jobs.go` (`buildCSV`)
**Description:** While using pagination (pages of 1,000), the entire CSV is buffered in a `bytes.Buffer` before being uploaded to S3.
**Risk:** For very large clinics with millions of audit events (common in long-term veterinary practices), a multi-gigabyte CSV could cause an OOM (Out of Memory) crash on the worker.
**Fix:** Use a multi-part upload or `io.Pipe` to stream the CSV rows directly to S3 as they are fetched from the database.

---

## 2. Data Integrity & SQL Performance

### [MISSING INDEX] High-Churn Job Tables
**Location:** `migrations/`
**Description:** The `note_events` table (created in `00012`) stores every audit action. 
**Risk:** The report generation worker (`internal/reports/jobs.go`) queries this table by `clinic_id` and date range. As the table grows to millions of rows, report generation will slow down exponentially without appropriate composite indexes.
**Fix:** Ensure composite indexes like `(clinic_id, occurred_at)` exist on `note_events`.

### [BUG] Broken Fuzzy Matching Algorithm
**Location:** `internal/platform/confidence/confidence.go` (`lcsRatio`)
**Description:** The implementation of `lcsRatio` is actually a **Longest Common Substring** algorithm (it resets `dp[j] = 0` on mismatch). The docstring and variable names imply it should be **Longest Common Subsequence**.
**Risk:** Longest Common Subsequence is significantly better for clinical transcripts where filler words ("um", "ah") or minor ASR errors might break a continuous substring. The current implementation is too strict, leading to lower-than-actual alignment scores and excessive "requires review" flags.
**Fix:** Standardize on Subsequence (remove the `else { dp[j] = 0 }`) or use a library like `Levenshtein`.

---

## 3. Security & Authentication

### [RISK] JWT Secret vs Key Derivation
**Location:** `internal/auth/service.go`
**Description:** The system uses a raw `jwtSecret` from config.
**Security Standard:** For SOC 2 compliance, it is preferable to use a Key Derivation Function (KDF) to separate the encryption key used for PII from the HMAC key used for JWT signing, even if they share the same root secret.
**Fix:** Derive separate keys using HKDF-SHA256 from the master secret.

### [MISSING] Rate Limiting on Magic Links
**Location:** `internal/auth/handler.go`
**Description:** No application-level rate limiting on `POST /api/v1/auth/magic-link`.
**Risk:** A malicious actor could script requests to send thousands of emails to a victim's address (Email Bombing) or exhaust the clinic's email quota.
**Fix:** Implement a leaky-bucket rate limiter based on IP and email address.

---

## 4. PII & Cryptography

### [BUG] Nonce Reuse Vulnerability (Potential)
**Location:** `internal/platform/crypto/crypto.go` (`Encrypt`)
**Description:** Uses `rand.Reader` for nonce generation (Good), but if the system entropy pool is exhausted in a containerized environment, GCM nonce reuse is catastrophic (leaks the key).
**Fix:** Ensure the production environment has a hardware-backed entropy source (like AWS Nitro or `/dev/urandom` mapped correctly).

### [PII LEAK] S3 Bucket Visibility
**Location:** `internal/platform/storage/storage.go`
**Description:** Report files are stored as `reports/{clinic_id}/{job_id}.csv`.
**Risk:** If the S3 bucket is accidentally marked as "Public" or a policy is misconfigured, every report in the system becomes guessable by UUID.
**Fix:** Use a bucket policy that strictly enforces `Deny` for non-IAM-authenticated requests and ensure bucket public access is blocked at the account level.

---

## 5. UI/UX & API Consistency

### [INCONSISTENCY] Status Enums
**Location:** `internal/domain/types.go`
**Description:** `NoteStatus` uses strings like `extracting`, `draft`, `submitted`.
**Improvement:** Ensure these are consistently returned as CamelCase or kebab-case to match frontend conventions (currently a mix).

### [DOCS] OpenAPI Security Mismatch
**Location:** `internal/app/app.go`
**Description:** `bearerAuth` is defined in the components but not applied globally to the API. 
**Result:** Every route must manually specify security, but many were missed (see previous audit).
**Fix:** Set a global security requirement in Huma config and only override it (to empty) for public routes.

---

## Summary of Priority (Updated)

| Severity | Issue | Area |
| :--- | :--- | :--- |
| **Critical** | AI Model Names (Gemini 2.5/GPT 4.1) | AI Extraction |
| **High** | Huma Context Wrapper Migration | Middleware |
| **High** | Broken LCS Algorithm | Transcription |
| **High** | Atomicity of Extraction Jobs | Backend Workflows |
| **Medium** | CSV Streaming for Reports | Scalability |
| **Medium** | Rate Limiting on Public Endpoints | Security |
