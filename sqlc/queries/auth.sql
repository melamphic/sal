-- name: CreateAuthToken :one
INSERT INTO auth_tokens (id, staff_id, token_hash, token_type, expires_at, created_from_ip)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAuthToken :one
SELECT * FROM auth_tokens
WHERE token_hash = $1;

-- name: MarkAuthTokenUsed :one
UPDATE auth_tokens
SET used_at = NOW()
WHERE token_hash = $1 AND used_at IS NULL
RETURNING *;

-- name: DeleteExpiredAuthTokens :exec
DELETE FROM auth_tokens
WHERE expires_at < NOW();

-- name: DeleteStaffAuthTokens :exec
-- Called on explicit logout to invalidate all refresh tokens for a staff member.
DELETE FROM auth_tokens
WHERE staff_id = $1 AND token_type = 'refresh';

-- name: CreateInviteToken :one
INSERT INTO invite_tokens (
    id, clinic_id, email, email_hash, role, permissions,
    note_tier, token_hash, expires_at, invited_by_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetInviteToken :one
SELECT * FROM invite_tokens
WHERE token_hash = $1 AND accepted_at IS NULL;

-- name: MarkInviteAccepted :one
UPDATE invite_tokens
SET accepted_at = NOW()
WHERE token_hash = $1
RETURNING *;

-- name: GetPendingInviteByEmailHash :one
SELECT * FROM invite_tokens
WHERE email_hash = $1
  AND clinic_id = $2
  AND accepted_at IS NULL
  AND expires_at > NOW();
