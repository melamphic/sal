-- +goose Up
-- +goose StatementBegin

-- Add manage_patients permission to staff.
-- Granted by default to all clinical roles — any authenticated staff member
-- who interacts with patients needs this. manage_billing/manage_staff remain restricted.
ALTER TABLE staff ADD COLUMN perm_manage_patients BOOLEAN NOT NULL DEFAULT false;

-- Backfill existing staff rows with the correct default for their role.
UPDATE staff SET perm_manage_patients = true
WHERE role IN ('super_admin', 'admin', 'vet', 'vet_nurse', 'receptionist');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE staff DROP COLUMN IF EXISTS perm_manage_patients;

-- +goose StatementEnd
