// Package approvals owns the second-pair-of-eyes ledger that any system
// widget can plug into. Each consuming domain (drugs, consent, incidents,
// pain) calls Service.Submit when it captures an entity that needs a
// review, and the same domain receives a status callback on
// approve/challenge so it can keep its own snapshot column in sync.
//
// The 4-file layout (repo / repository / service / handler / routes)
// matches the project's standard architecture rules.
package approvals

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// repo is the internal data-access interface. The concrete implementation
// is in repository.go; tests use fakeRepo.
type repo interface {
	Create(ctx context.Context, p CreateParams) (*Record, error)
	GetByID(ctx context.Context, id, clinicID uuid.UUID) (*Record, error)
	// Decide transitions a pending row to approved/challenged. Returns
	// domain.ErrConflict if the row isn't in pending state, ErrNotFound
	// if the row doesn't exist for the given clinic.
	Decide(ctx context.Context, p DecideParams) (*Record, error)
	// ListPending returns pending rows for the clinic, optionally
	// filtered by entity_kind. Excludes rows submitted by the
	// requesting staff member (you can't approve your own).
	ListPending(ctx context.Context, p ListPendingParams) ([]*Record, error)
	// GetLatestForEntity returns the most recent approval row for an
	// entity (used to render the per-entity audit trail).
	GetLatestForEntity(ctx context.Context, kind domain.ApprovalEntityKind, entityID uuid.UUID) (*Record, error)
	// CountPendingForDecider returns how many pending rows the staff
	// member could act on. Drives the dashboard "N approvals waiting"
	// chip.
	CountPendingForDecider(ctx context.Context, clinicID, staffID uuid.UUID) (int, error)
	// ListPendingForSubject returns pending rows scoped to one subject.
	// Powers the subject-hub "Pending compliance" card so a clinician
	// reviewing a patient sees what is still waiting on someone.
	ListPendingForSubject(ctx context.Context, clinicID, subjectID uuid.UUID, limit int) ([]*Record, error)
}

// Record is the raw DB representation of a compliance_approvals row.
type Record struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	EntityKind domain.ApprovalEntityKind
	EntityID   uuid.UUID
	EntityOp   *string
	Status     domain.ApprovalStatus

	SubmittedBy   uuid.UUID
	SubmittedAt   time.Time
	SubmittedNote *string

	DeadlineAt time.Time

	DecidedBy      *uuid.UUID
	DecidedAt      *time.Time
	DecidedComment *string

	SubjectID *uuid.UUID
	NoteID    *uuid.UUID

	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateParams holds values for inserting a new pending approval row.
// Service callers pre-validate that submitter has permission to log the
// entity; the approvals package itself does not enforce per-kind rules
// (delegated to consuming domain).
type CreateParams struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	EntityKind    domain.ApprovalEntityKind
	EntityID      uuid.UUID
	EntityOp      *string
	SubmittedBy   uuid.UUID
	SubmittedNote *string
	DeadlineAt    time.Time
	SubjectID     *uuid.UUID
	NoteID        *uuid.UUID
}

// DecideParams transitions an approval row away from pending. The
// service layer enforces decider != submitter and decider has the
// per-kind permission before invoking the repo.
type DecideParams struct {
	ID             uuid.UUID
	ClinicID       uuid.UUID
	NewStatus      domain.ApprovalStatus // approved | challenged
	DecidedBy      uuid.UUID
	DecidedAt      time.Time
	DecidedComment *string
}

// ListPendingParams scope the queue lookup. EntityKind is optional; nil
// returns every kind so the unified queue page can render mixed rows.
type ListPendingParams struct {
	ClinicID         uuid.UUID
	EntityKind       *domain.ApprovalEntityKind
	ExcludeSubmitter uuid.UUID // can't approve your own
	Limit            int
}
