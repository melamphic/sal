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
	// PublishDraftVersion freezes the draft: sets status=published, assigns
	// version_major/minor, and records who published it and when.
	PublishDraftVersion(ctx context.Context, p PublishDraftVersionParams) (*FormVersionRecord, error)
	// SavePolicyCheckResult stores the raw AI policy-check output on the draft.
	SavePolicyCheckResult(ctx context.Context, p SavePolicyCheckParams) (*FormVersionRecord, error)

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
