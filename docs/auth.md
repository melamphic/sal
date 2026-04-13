# Authentication

Salvia uses a **passwordless magic link** flow with JWT access tokens and opaque refresh tokens. There are no passwords.

---

## Flow overview

```
1. User enters email → POST /api/v1/auth/magic-link
2. Server hashes email → looks up staff by hash
3. If found and active → generate opaque token → hash it → store hash
4. Send email with raw token in URL
5. User clicks link → GET /api/v1/auth/verify?token=<raw>
6. Server hashes raw token → fetches stored hash → validates (expiry, single-use)
7. Returns: { access_token, refresh_token, expires_at }
8. Client uses access_token in Authorization: Bearer header
9. When access_token expires → POST /api/v1/auth/refresh with refresh_token
10. Logout → POST /api/v1/auth/logout → deletes all refresh tokens for staff
```

---

## Token types

### Magic link token

- **Format:** opaque random bytes, URL-safe base64 encoded
- **Storage:** SHA-256 hash stored in `auth_tokens`, raw token sent in email only
- **TTL:** 15 minutes (configurable via `MAGIC_LINK_TTL`)
- **Single-use:** `used_at` is set atomically in a `FOR UPDATE` transaction on first consumption
- **Token type:** `magic_link` in `auth_tokens.token_type`

### Access token (JWT)

- **Format:** HS256 JWT signed with `JWT_SECRET`
- **Claims:**
  ```json
  {
    "iss": "sal",
    "sub": "<staff_id>",
    "exp": <unix_timestamp>,
    "iat": <unix_timestamp>,
    "clinic_id": "<uuid>",
    "staff_id": "<uuid>",
    "role": "vet",
    "perms": { "manage_staff": false, ... }
  }
  ```
- **TTL:** 15 minutes (configurable via `JWT_ACCESS_TTL`)
- **Not stored in DB** — stateless, validated by signature

### Refresh token

- **Format:** opaque random bytes, same generation as magic link
- **Storage:** SHA-256 hash stored in `auth_tokens` with type `refresh`
- **TTL:** 30 days (configurable via `JWT_REFRESH_TTL`)
- **Rotation:** each use issues a new refresh token and marks the old one as used
- **Logout:** all refresh tokens for a staff member are deleted

---

## Security properties

| Property | How it's achieved |
|---|---|
| Email enumeration prevention | Unknown emails return 200 with no email sent |
| Token interception on storage | Raw tokens are never stored — only SHA-256 hashes |
| Replay prevention | Single-use enforced via `FOR UPDATE` + `used_at` in one transaction |
| Type confusion | `token_type` checked before using a token — refresh token cannot be used as magic link and vice versa |
| Deactivated staff | `status = 'active'` check before sending magic link |
| JWT forgery | HS256 with 32+ byte secret; algorithm validated on parse |

---

## PII handling

Email addresses are stored **AES-256-GCM encrypted** in the `email` column. A separate **HMAC-SHA256 hash** of the normalised email (lowercased, trimmed) is stored in `email_hash` for equality lookups without decrypting. The auth module never stores a plaintext email.

---

## Permissions

Permissions are embedded in the JWT `perms` claim so handlers can authorise without a DB round-trip on every request. The `platform/middleware` package provides:

```go
// Chi middleware — protects a route group
r.Use(middleware.Authenticate(jwtSecret))

// Huma operation middleware — protects a single operation
huma.Middlewares{
    middleware.RequirePermissionHuma(api, func(p domain.Permissions) bool {
        return p.ManageStaff
    }),
}
```

---

## Database schema

```sql
CREATE TABLE auth_tokens (
    id              UUID PRIMARY KEY,
    staff_id        UUID NOT NULL REFERENCES staff(id),
    token_hash      TEXT NOT NULL UNIQUE,   -- SHA-256 of raw token
    token_type      VARCHAR NOT NULL CHECK (token_type IN ('magic_link', 'refresh')),
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,            -- NULL = unused
    created_from_ip TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## Configuration

| Env var | Default | Description |
|---|---|---|
| `JWT_SECRET` | required | HMAC secret for signing access tokens (min 32 bytes) |
| `JWT_ACCESS_TTL` | `15m` | Access token lifetime |
| `JWT_REFRESH_TTL` | `720h` | Refresh token lifetime (30 days) |
| `MAGIC_LINK_TTL` | `15m` | Magic link expiry |
| `APP_URL` | required | Base URL used to construct magic link (e.g. `https://app.salvia.io`) |
