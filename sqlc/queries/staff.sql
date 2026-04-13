-- name: CreateStaff :one
INSERT INTO staff (
    id, clinic_id, email, email_hash, full_name, role, note_tier,
    perm_manage_staff, perm_manage_forms, perm_manage_policies,
    perm_manage_billing, perm_rollback_policies, perm_record_audio,
    perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
    perm_dispense, perm_generate_audit_export,
    status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13,
    $14, $15, $16, $17, $18,
    $19
)
RETURNING *;

-- name: GetStaffByID :one
SELECT * FROM staff
WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL;

-- name: GetStaffByEmailHash :one
SELECT * FROM staff
WHERE email_hash = $1 AND archived_at IS NULL;

-- name: GetStaffByEmailHashAndClinic :one
SELECT * FROM staff
WHERE email_hash = $1 AND clinic_id = $2 AND archived_at IS NULL;

-- name: ListStaff :many
SELECT * FROM staff
WHERE clinic_id = $1 AND archived_at IS NULL
ORDER BY created_at ASC
LIMIT $2 OFFSET $3;

-- name: CountStaff :one
SELECT COUNT(*) FROM staff
WHERE clinic_id = $1 AND archived_at IS NULL;

-- name: UpdateStaffPermissions :one
UPDATE staff SET
    perm_manage_staff          = $3,
    perm_manage_forms          = $4,
    perm_manage_policies       = $5,
    perm_manage_billing        = $6,
    perm_rollback_policies     = $7,
    perm_record_audio          = $8,
    perm_submit_forms          = $9,
    perm_view_all_patients     = $10,
    perm_view_own_patients     = $11,
    perm_dispense              = $12,
    perm_generate_audit_export = $13,
    updated_at                 = NOW()
WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
RETURNING *;

-- name: UpdateStaffLastActive :exec
UPDATE staff SET last_active_at = NOW(), status = 'active'
WHERE id = $1;

-- name: DeactivateStaff :one
UPDATE staff SET status = 'deactivated', updated_at = NOW()
WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
RETURNING *;

-- name: CountVetsByClinic :one
-- Used by billing to calculate the clinic's pricing tier.
SELECT COUNT(*) FROM staff
WHERE clinic_id = $1
  AND note_tier = 'standard'
  AND status = 'active'
  AND archived_at IS NULL;
