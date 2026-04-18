# Salvia Backend Security & Architecture Audit

This audit document identifies architectural risks, security gaps, and implementation bugs discovered during a line-by-line review of the Salvia codebase.

## 1. Authentication & Middleware

### [ISSUE] Huma/Chi Context Desync (High Risk)
**Location:** Multiple `internal/**/routes.go`
**Description:** Most modules use standard Chi `r.Group` and `r.Use(mw.Authenticate)` to wrap Huma operations.
**Risk:** 
- **Context Loss:** Huma creates its own `huma.Context`. If Chi modifies the request context *after* Huma has initialized, the Huma handler may receive a stale context.
- **OpenAPI Inaccuracy:** Chi middleware is invisible to the Huma OpenAPI generator. The resulting Swagger UI does not show that these routes require authentication, nor does it provide the "Authorize" button.
**Fix:** Use the newly implemented `mw.AuthenticateHuma(api, jwtSecret)` directly in the `huma.Operation` definition.

### [ISSUE] Missing Security Schema in OpenAPI (Medium Risk)
**Location:** `internal/**/routes.go`
**Description:** Almost all `huma.Operation` definitions are missing the `Security` field.
**Risk:** The API documentation is technically incomplete. Automated client generators will not include authorization headers for these endpoints.
**Fix:** Add `Security: []map[string][]string{{"bearerAuth": {}}}` to all protected operations.

---

## 2. Mailer (`internal/platform/mailer/mailer.go`)

### [BUG] Potential Hang on SMTPS (Port 465)
**Location:** `mailer.go` L104-L141
**Description:** The code manually dials TLS and creates an `smtp.NewClient`. 
**Risk:** 
- **Timeouts:** There is no `SetDeadline` on the connection. If the SMTP server accepts the TCP connection but hangs during the TLS handshake, the Go routine will hang indefinitely.
- **TLS Config:** The `ServerName` is set correctly, but `MinVersion` should be enforced to `VersionTLS12` (which it is), but many modern relays (like Resend) prefer `VersionTLS13`.
**Fix:** Wrap the dialer with a timeout context.

### [BUG] Insecure Plain SMTP fallback
**Location:** `mailer.go` L144
**Description:** For ports other than 465 (like 587), the code uses `smtp.SendMail`.
**Risk:** `smtp.SendMail` automatically attempts STARTTLS if the server supports it, but if the server doesn't, it falls back to **Plaintext Authentication** over the internet.
**Fix:** Use a custom sender that enforces `STARTTLS` and rejects unencrypted connections for port 587.

---

## 3. Storage & Audio (`internal/platform/storage/storage.go`)

### [BUG] Content-Type Signature Mismatch
**Location:** `storage.go` L60 (PresignUpload)
**Description:** The signature includes the `ContentType`. 
**Risk:** If the frontend doesn't send the exact MIME type (e.g., `audio/mp4` vs `audio/m4a`) that was used during the presign call, S3/MinIO will return `403 Forbidden`. The current `extensionFor` helper in `audio/service.go` is basic.
**Fix:** Ensure the frontend explicitly passes the desired Content-Type or use a strictly validated list.

### [ARCHITECTURAL RISK] Large Memory Usage in Dev
**Location:** `audio/transcriber.go` L146 (`downloadBytes`)
**Description:** The `GeminiTranscriber` (used in dev/staging) downloads the entire audio file into a `[]byte` in memory.
**Risk:** While fine for 5-minute clips, a 60-minute WAV file can exceed 500MB. Multiple concurrent transcriptions in a low-RAM environment (like a small dev VPS) will cause an OOM (Out Of Memory) crash.
**Fix:** Use an `io.Reader` or temporary file for Gemini extraction if possible (Gemini API supports file URI).

---

## 4. AI & Extraction (`internal/extraction/`)

### [CRITICAL BUG] Non-Existent Model Name
**Location:** `gemini.go` L11, `transcriber.go` L88
**Description:** The code uses `gemini-2.5-flash`.
**Risk:** **Gemini 2.5 does not exist.** The current latest models are `gemini-1.5-flash` or `gemini-2.0-flash-exp`. This code will return `404 Not Found` or `400 Bad Request` from the Google API immediately.
**Fix:** Update constant to `gemini-1.5-flash` or `gemini-2.0-flash`.

### [BUG] Non-Existent OpenAI Model
**Location:** `openai.go` L12
**Description:** The code uses `openai.ChatModelGPT4_1Mini` (hypothetical GPT-4.1).
**Risk:** There is no GPT-4.1-mini. The current model is `gpt-4o-mini`.
**Fix:** Use `gpt-4o-mini`.

### [BUG] Brittle JSON Parsing (Gemini)
**Location:** `gemini.go` L216-L221
**Description:** Manual stripping of markdown fences.
**Risk:** If Gemini returns text *before* the JSON (e.g., "Here is your data: ```json..."), the `TrimPrefix` logic will fail.
**Fix:** Use a regex or a more robust JSON locator that finds the first `[` and last `]`.

---

## 5. Staff & Clinic Logic

### [BUG] ID Collision Risk on registration
**Location:** `clinic/service.go` L139
**Description:** `generateSlug` truncates to 60 chars but doesn't handle collisions.
**Risk:** If two clinics register as "Central Veterinary Clinic", the second will fail with a DB unique constraint violation on the `slug` column.
**Fix:** Add a random 4-char suffix to the slug if a collision is detected in the repository.

### [SECURITY] PII Leak in Logs
**Location:** `staff/service.go` L109 (Invite)
**Description:** `fmt.Sprintf("%s/invite/accept", s.appURL)` is used, but the email is not included in the URL as a hint.
**Fix:** This is actually good (prevents PII in server logs via Referer headers), but ensures the acceptance page can handle the token without knowing the email beforehand.

---

## Summary of Priority
1. **Critical:** Fix AI Model Names (Gemini 2.5 / GPT 4.1).
2. **High:** Migrate to `AuthenticateHuma` to fix context issues and OpenAPI docs.
3. **High:** Fix SMTP hanging by adding timeouts to TLS dials.
4. **Medium:** Fix memory usage in `downloadBytes` for audio.
