-- name: CreateClinic :one
INSERT INTO clinics (
    id, name, slug, email, email_hash, phone, address,
    vertical, status, trial_ends_at, data_region
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: GetClinicByID :one
SELECT * FROM clinics
WHERE id = $1 AND archived_at IS NULL;

-- name: GetClinicBySlug :one
SELECT * FROM clinics
WHERE slug = $1 AND archived_at IS NULL;

-- name: GetClinicByEmailHash :one
SELECT * FROM clinics
WHERE email_hash = $1 AND archived_at IS NULL;

-- name: UpdateClinic :one
UPDATE clinics
SET
    name       = COALESCE(sqlc.narg(name), name),
    phone      = COALESCE(sqlc.narg(phone), phone),
    address    = COALESCE(sqlc.narg(address), address),
    updated_at = NOW()
WHERE id = $1 AND archived_at IS NULL
RETURNING *;

-- name: UpdateClinicStatus :one
UPDATE clinics
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: IncrementNoteCount :one
UPDATE clinics
SET note_count = note_count + 1, updated_at = NOW()
WHERE id = $1
RETURNING note_count, note_cap;
