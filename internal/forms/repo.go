package forms

import (
	"context"

	"github.com/google/uuid"
)

// repo is the internal data-access interface for the forms module.
// The concrete implementation is in repository.go; tests use fakeRepo.
type repo interface {
	// ── Groups ────────────────────────────────────────────────────────────────

	CreateGroup(ctx context.Context, p CreateGroupParams) (*GroupRecord, error)
	GetGroupByID(ctx context.Context, id, clinicID uuid.UUID) (*GroupRecord, error)
	ListGroups(ctx context.Context, clinicID uuid.UUID) ([]*GroupRecord, error)
	UpdateGroup(ctx context.Context, p UpdateGroupParams) (*GroupRecord, error)

	// ── Forms ─────────────────────────────────────────────────────────────────

	CreateForm(ctx context.Context, p CreateFormParams) (*FormRecord, error)
	GetFormByID(ctx context.Context, id, clinicID uuid.UUID) (*FormRecord, error)
	ListForms(ctx context.Context, clinicID uuid.UUID, p ListFormsParams) ([]*FormRecord, int, error)
	UpdateFormMeta(ctx context.Context, p UpdateFormMetaParams) (*FormRecord, error)
	// RetireForm sets archived_at and retire_reason. It does not delete any rows.
	RetireForm(ctx context.Context, p RetireFormParams) (*FormRecord, error)

	// ── Versions ──────────────────────────────────────────────────────────────

	// GetDraftVersion returns the single mutable draft for a form.
	// Returns domain.ErrNotFound if no draft exists.
	GetDraftVersion(ctx context.Context, formID uuid.UUID) (*FormVersionRecord, error)
	GetVersionByID(ctx context.Context, id uuid.UUID) (*FormVersionRecord, error)
	ListPublishedVersions(ctx context.Context, formID uuid.UUID) ([]*FormVersionRecord, error)
	// GetLatestPublishedVersion returns the most recently published version.
	// Returns domain.ErrNotFound if no published version exists yet.
	GetLatestPublishedVersion(ctx context.Context, formID uuid.UUID) (*FormVersionRecord, error)
	// CreateDraftVersion inserts a new draft row. Errors with domain.ErrConflict
	// if a draft already exists (enforced by DB partial unique index).
	CreateDraftVersion(ctx context.Context, p CreateDraftVersionParams) (*FormVersionRecord, error)
	// CreateFormWithDraft atomically inserts a form row and its initial empty
	// draft version inside a single transaction so a partial failure can
	// never leave a form without a draft (the "zombie form" state).
	CreateFormWithDraft(ctx context.Context, p CreateFormWithDraftParams) (*FormRecord, *FormVersionRecord, error)
	// DeleteDraftVersion deletes the current draft version of a form. Returns
	// domain.ErrNotFound if no draft exists.
	DeleteDraftVersion(ctx context.Context, formID uuid.UUID) error
	// PublishDraftVersion freezes the draft: sets status=published, assigns
	// version_major/minor, and records who published it and when.
	PublishDraftVersion(ctx context.Context, p PublishDraftVersionParams) (*FormVersionRecord, error)
	// CreatePublishedVersionWithFields inserts a new version already in the
	// published state along with its fields in a single transaction. Used by
	// rollback, which produces a fresh immutable version rather than mutating
	// or discarding any existing draft. Returns domain.ErrConflict if the
	// (form_id, version_major, version_minor) pair collides with an existing
	// published version — caller should recompute and retry.
	CreatePublishedVersionWithFields(ctx context.Context, p CreatePublishedVersionParams, fields []CreateFieldParams) (*FormVersionRecord, []*FieldRecord, error)
	// SavePolicyCheckResult stores the raw AI policy-check output on the draft.
	SavePolicyCheckResult(ctx context.Context, p SavePolicyCheckParams) (*FormVersionRecord, error)
	// UpdateDraftSystemHeader replaces the system_header_config JSONB on a draft.
	// clinicID enforces tenant isolation — passing the wrong tenant returns ErrNotFound.
	// No-op for non-draft versions (published rows are immutable).
	UpdateDraftSystemHeader(ctx context.Context, versionID, clinicID uuid.UUID, config []byte) (*FormVersionRecord, error)

	// SaveGenerationMetadata stores the AI-generation provenance JSONB on a
	// form_version row. Used by aigen-driven flows to mark a draft as
	// AI-authored so the editor can render the "AI drafted" badge and the
	// audit log captures provider/model/prompt-hash. metadata may be nil to
	// clear the column. clinicID enforces tenant isolation.
	SaveGenerationMetadata(ctx context.Context, versionID, clinicID uuid.UUID, metadata []byte) error

	// ── Fields ────────────────────────────────────────────────────────────────

	GetFieldsByVersionID(ctx context.Context, versionID uuid.UUID) ([]*FieldRecord, error)
	// ReplaceFields deletes all existing fields for versionID then inserts the
	// new set in a single transaction. Used for bulk draft field updates.
	ReplaceFields(ctx context.Context, versionID uuid.UUID, fields []CreateFieldParams) ([]*FieldRecord, error)

	// ── Policies ──────────────────────────────────────────────────────────────

	LinkPolicy(ctx context.Context, formID, policyID, linkedBy uuid.UUID) error
	UnlinkPolicy(ctx context.Context, formID, policyID uuid.UUID) error
	ListLinkedPolicies(ctx context.Context, formID uuid.UUID) ([]uuid.UUID, error)
	// ListFormIDsByPolicyID returns all form IDs that have the given policy linked.
	// Used by the policy engine when retiring a policy to remove all links.
	ListFormIDsByPolicyID(ctx context.Context, policyID uuid.UUID) ([]uuid.UUID, error)
	// ListPolicyUnlinkEvents returns soft-unlinked form_policies rows for a form.
	// Used by ListVersions to inject synthetic "Policy X unlinked" trail entries.
	ListPolicyUnlinkEvents(ctx context.Context, formID uuid.UUID) ([]*PolicyUnlinkEventRecord, error)

	// ── Style ─────────────────────────────────────────────────────────────────

	// GetCurrentStyle returns the active style version for the clinic.
	// Returns domain.ErrNotFound if no style has been configured yet.
	GetCurrentStyle(ctx context.Context, clinicID uuid.UUID) (*StyleVersionRecord, error)
	// ListStyleVersions returns every style version for a clinic, newest first.
	ListStyleVersions(ctx context.Context, clinicID uuid.UUID) ([]*StyleVersionRecord, error)
	// CreateStyleVersion inserts a new style version row (version = prev+1) and
	// marks it active, demoting any previously-active row for the clinic.
	CreateStyleVersion(ctx context.Context, p CreateStyleVersionParams) (*StyleVersionRecord, error)
}
