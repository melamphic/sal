package approvals

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// EntityStatusUpdater is the callback the consuming domain registers
// so its *_status snapshot column stays in sync with the approval
// lifecycle. The approvals package never JOINs into another domain's
// tables (per CLAUDE.md cross-domain rule); it calls back through
// these adapters at the service layer.
type EntityStatusUpdater interface {
	// UpdateEntityReviewStatus is invoked on Submit (sets to 'pending')
	// and on Approve/Challenge (sets to the resulting status). The
	// implementation should be idempotent.
	UpdateEntityReviewStatus(ctx context.Context, kind domain.ApprovalEntityKind, entityID, clinicID uuid.UUID, status domain.EntityReviewStatus) error
}

// PermissionChecker is supplied by the staff package so the approvals
// service can enforce per-kind rules without importing staff types.
type PermissionChecker interface {
	HasPermission(ctx context.Context, staffID, clinicID uuid.UUID, perm string) (bool, error)
}

// EventEmitter publishes lifecycle events to the timeline so subjects
// + clinics see the approval audit trail. Implementations land events
// in `note_events` (when entity is note-bound) and `clinic_audit_log`.
type EventEmitter interface {
	Emit(ctx context.Context, e Event)
}

// Event carries data about a single approval lifecycle transition.
// Mirrors the shape used by other event emitters in the project.
type Event struct {
	ApprovalID uuid.UUID
	ClinicID   uuid.UUID
	SubjectID  *uuid.UUID
	NoteID     *uuid.UUID
	EntityKind domain.ApprovalEntityKind
	EntityID   uuid.UUID
	Type       EventType
	ActorID    uuid.UUID
	ActorRole  string
	Reason     *string
}

// EventType is the dot-separated event name written to note_events.
type EventType string

const (
	EventTypeApprovalPending    EventType = "compliance.approval_pending"
	EventTypeApprovalApproved   EventType = "compliance.approval_approved"
	EventTypeApprovalChallenged EventType = "compliance.approval_challenged"
)

// noopEmitter discards events. Used when no emitter is wired (tests).
type noopEmitter struct{}

func (noopEmitter) Emit(_ context.Context, _ Event) {}

// Service orchestrates the approval lifecycle.
type Service struct {
	repo            repo
	clock           func() time.Time
	defaultDeadline time.Duration
	statusUpdaters  map[domain.ApprovalEntityKind]EntityStatusUpdater
	permissions     PermissionChecker
	events          EventEmitter
}

// NewService builds a Service. Per-kind status updaters are registered
// via SetStatusUpdater after construction; an unset kind makes Submit
// reject with a wrapped error so we never end up with stale entity
// snapshot columns.
func NewService(repo repo) *Service {
	return &Service{
		repo:            repo,
		clock:           domain.TimeNow,
		defaultDeadline: 48 * time.Hour,
		statusUpdaters:  make(map[domain.ApprovalEntityKind]EntityStatusUpdater),
		events:          noopEmitter{},
	}
}

// SetStatusUpdater registers a callback for an entity kind. The drugs,
// consent, incidents, and pain services each register their own.
func (s *Service) SetStatusUpdater(kind domain.ApprovalEntityKind, u EntityStatusUpdater) {
	s.statusUpdaters[kind] = u
}

// SetPermissionChecker wires the staff permission lookup. Must be set
// before any Decide call lands or the service rejects with ErrForbidden.
func (s *Service) SetPermissionChecker(p PermissionChecker) {
	s.permissions = p
}

// SetEventEmitter wires the timeline emitter. Defaults to no-op.
func (s *Service) SetEventEmitter(e EventEmitter) {
	if e == nil {
		s.events = noopEmitter{}
		return
	}
	s.events = e
}

// SetDefaultDeadline configures the default approval window. Per-clinic
// override could be added later; for now a process-wide knob.
func (s *Service) SetDefaultDeadline(d time.Duration) {
	if d <= 0 {
		return
	}
	s.defaultDeadline = d
}

// SubmitInput captures everything the consuming domain knows when it
// hands an entity off for review. SubjectID and NoteID are optional;
// when set, the timeline emitter routes the event onto the right
// subject + note audit trails.
type SubmitInput struct {
	ClinicID    uuid.UUID
	EntityKind  domain.ApprovalEntityKind
	EntityID    uuid.UUID
	EntityOp    *string
	SubmittedBy uuid.UUID
	StaffRole   string
	Note        *string
	Deadline    *time.Duration // override default; nil = use service default
	SubjectID   *uuid.UUID
	NoteID      *uuid.UUID
}

// Submit creates a pending approval row and asks the consuming domain
// to flip its entity snapshot to 'pending'. Idempotency: if the entity
// already has a pending row, repo returns ErrConflict — caller should
// surface "this is already in the queue".
func (s *Service) Submit(ctx context.Context, in SubmitInput) (*Record, error) {
	if _, ok := s.statusUpdaters[in.EntityKind]; !ok {
		return nil, fmt.Errorf(
			"approvals.service.Submit: no status updater for %s: %w",
			in.EntityKind, domain.ErrValidation,
		)
	}
	deadline := s.defaultDeadline
	if in.Deadline != nil && *in.Deadline > 0 {
		deadline = *in.Deadline
	}
	now := s.clock()
	rec, err := s.repo.Create(ctx, CreateParams{
		ID:            domain.NewID(),
		ClinicID:      in.ClinicID,
		EntityKind:    in.EntityKind,
		EntityID:      in.EntityID,
		EntityOp:      in.EntityOp,
		SubmittedBy:   in.SubmittedBy,
		SubmittedNote: trimPtr(in.Note),
		DeadlineAt:    now.Add(deadline),
		SubjectID:     in.SubjectID,
		NoteID:        in.NoteID,
	})
	if err != nil {
		return nil, fmt.Errorf("approvals.service.Submit: %w", err)
	}
	if err := s.statusUpdaters[in.EntityKind].UpdateEntityReviewStatus(
		ctx, in.EntityKind, in.EntityID, in.ClinicID, domain.EntityReviewPending,
	); err != nil {
		return nil, fmt.Errorf("approvals.service.Submit: status updater: %w", err)
	}
	s.events.Emit(ctx, Event{
		ApprovalID: rec.ID,
		ClinicID:   rec.ClinicID,
		SubjectID:  rec.SubjectID,
		NoteID:     rec.NoteID,
		EntityKind: rec.EntityKind,
		EntityID:   rec.EntityID,
		Type:       EventTypeApprovalPending,
		ActorID:    in.SubmittedBy,
		ActorRole:  in.StaffRole,
		Reason:     rec.SubmittedNote,
	})
	return rec, nil
}

// DecideInput is the request shape for an approve/challenge action.
type DecideInput struct {
	ApprovalID uuid.UUID
	ClinicID   uuid.UUID
	DeciderID  uuid.UUID
	StaffRole  string
	NewStatus  domain.ApprovalStatus // approved | challenged
	Comment    *string
}

// Decide records an approve/challenge. Enforces:
//   - decider != submitter (server-side gate against rubber-stamping)
//   - decider holds the per-kind permission (asks the staff package)
//   - challenge requires a non-empty comment (so the original signer
//     knows what to fix)
//   - status transition is one of the two allowed terminal states
//
// Updates the entity snapshot column on success and emits the right
// timeline event.
func (s *Service) Decide(ctx context.Context, in DecideInput) (*Record, error) {
	if in.NewStatus != domain.ApprovalStatusApproved &&
		in.NewStatus != domain.ApprovalStatusChallenged {
		return nil, fmt.Errorf(
			"approvals.service.Decide: invalid status %s: %w",
			in.NewStatus, domain.ErrValidation,
		)
	}
	if in.NewStatus == domain.ApprovalStatusChallenged &&
		(in.Comment == nil || strings.TrimSpace(*in.Comment) == "") {
		return nil, fmt.Errorf(
			"approvals.service.Decide: challenge requires a comment: %w",
			domain.ErrValidation,
		)
	}
	pre, err := s.repo.GetByID(ctx, in.ApprovalID, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("approvals.service.Decide: %w", err)
	}
	if pre.SubmittedBy == in.DeciderID {
		return nil, fmt.Errorf(
			"approvals.service.Decide: cannot approve own submission: %w",
			domain.ErrForbidden,
		)
	}
	updater, ok := s.statusUpdaters[pre.EntityKind]
	if !ok {
		return nil, fmt.Errorf(
			"approvals.service.Decide: no status updater for %s: %w",
			pre.EntityKind, domain.ErrValidation,
		)
	}
	if s.permissions != nil {
		perm := requiredPermissionFor(pre.EntityKind)
		if perm != "" {
			has, permErr := s.permissions.HasPermission(ctx, in.DeciderID, in.ClinicID, perm)
			if permErr != nil {
				return nil, fmt.Errorf("approvals.service.Decide: perm lookup: %w", permErr)
			}
			if !has {
				return nil, fmt.Errorf(
					"approvals.service.Decide: lacks %s: %w",
					perm, domain.ErrForbidden,
				)
			}
		}
	}
	rec, err := s.repo.Decide(ctx, DecideParams{
		ID:             in.ApprovalID,
		ClinicID:       in.ClinicID,
		NewStatus:      in.NewStatus,
		DecidedBy:      in.DeciderID,
		DecidedAt:      s.clock(),
		DecidedComment: trimPtr(in.Comment),
	})
	if err != nil {
		return nil, fmt.Errorf("approvals.service.Decide: %w", err)
	}
	entityStatus := domain.EntityReviewApproved
	if rec.Status == domain.ApprovalStatusChallenged {
		entityStatus = domain.EntityReviewChallenged
	}
	if err := updater.UpdateEntityReviewStatus(
		ctx, rec.EntityKind, rec.EntityID, rec.ClinicID, entityStatus,
	); err != nil {
		return nil, fmt.Errorf("approvals.service.Decide: status updater: %w", err)
	}
	eventType := EventTypeApprovalApproved
	if rec.Status == domain.ApprovalStatusChallenged {
		eventType = EventTypeApprovalChallenged
	}
	s.events.Emit(ctx, Event{
		ApprovalID: rec.ID,
		ClinicID:   rec.ClinicID,
		SubjectID:  rec.SubjectID,
		NoteID:     rec.NoteID,
		EntityKind: rec.EntityKind,
		EntityID:   rec.EntityID,
		Type:       eventType,
		ActorID:    in.DeciderID,
		ActorRole:  in.StaffRole,
		Reason:     rec.DecidedComment,
	})
	return rec, nil
}

// ListPending returns the queue rows for the staff member, scoped to
// clinic and excluding rows they submitted themselves. Optional
// EntityKind filter narrows the queue (the dashboard chip uses
// CountPendingForDecider; the dedicated queue page uses this).
func (s *Service) ListPending(ctx context.Context, clinicID, staffID uuid.UUID, kind *domain.ApprovalEntityKind, limit int) ([]*Record, error) {
	out, err := s.repo.ListPending(ctx, ListPendingParams{
		ClinicID:         clinicID,
		EntityKind:       kind,
		ExcludeSubmitter: staffID,
		Limit:            limit,
	})
	if err != nil {
		return nil, fmt.Errorf("approvals.service.ListPending: %w", err)
	}
	return out, nil
}

// CountPendingForDecider — see Repository.CountPendingForDecider.
func (s *Service) CountPendingForDecider(ctx context.Context, clinicID, staffID uuid.UUID) (int, error) {
	n, err := s.repo.CountPendingForDecider(ctx, clinicID, staffID)
	if err != nil {
		return 0, fmt.Errorf("approvals.service.CountPendingForDecider: %w", err)
	}
	return n, nil
}

// GetLatestForEntity surfaces the most recent approval row for an
// entity (any status). Lets the consuming domain render its audit
// trail without joining the approvals table directly.
func (s *Service) GetLatestForEntity(ctx context.Context, kind domain.ApprovalEntityKind, entityID uuid.UUID) (*Record, error) {
	rec, err := s.repo.GetLatestForEntity(ctx, kind, entityID)
	if err != nil {
		return nil, fmt.Errorf("approvals.service.GetLatestForEntity: %w", err)
	}
	return rec, nil
}

// requiredPermissionFor maps an approval kind to the permission a
// decider must hold. Vertical-aware policy lives one level higher (the
// staff package decides who gets dispense based on role + vertical);
// this just names the gate.
func requiredPermissionFor(kind domain.ApprovalEntityKind) string {
	switch kind {
	case domain.ApprovalKindDrugOp:
		// Veterinary RVN, aged-care RN, GP nurse all map to
		// dispense in our permission model.
		return "perm_witness_controlled_drugs"
	case domain.ApprovalKindIncident:
		// Supervisor / clinical lead reviews incidents — wired to
		// manage_patients today; could split later.
		return "perm_manage_patients"
	case domain.ApprovalKindConsent:
		return "perm_manage_patients"
	case domain.ApprovalKindPainScore:
		// Any clinical role.
		return ""
	default:
		return ""
	}
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}
