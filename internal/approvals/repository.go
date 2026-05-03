package approvals

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// Repository is the PostgreSQL implementation of the repo interface.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository builds a Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const approvalCols = `id, clinic_id, entity_kind, entity_id, entity_op, status,
	submitted_by, submitted_at, submitted_note, deadline_at,
	decided_by, decided_at, decided_comment,
	subject_id, note_id, created_at, updated_at`

// Create inserts a pending approval row.
func (r *Repository) Create(ctx context.Context, p CreateParams) (*Record, error) {
	q := fmt.Sprintf(`
		INSERT INTO compliance_approvals
			(id, clinic_id, entity_kind, entity_id, entity_op, status,
			 submitted_by, submitted_note, deadline_at, subject_id, note_id)
		VALUES ($1,$2,$3,$4,$5,'pending',$6,$7,$8,$9,$10)
		RETURNING %s`, approvalCols)

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, string(p.EntityKind), p.EntityID, p.EntityOp,
		p.SubmittedBy, p.SubmittedNote, p.DeadlineAt, p.SubjectID, p.NoteID,
	)
	rec, err := scanApproval(row)
	if err != nil {
		// Unique-pending index collision means the entity already has
		// an open approval; surface as conflict so the caller can
		// surface "this is already in the queue".
		if domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("approvals.repo.Create: already pending: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("approvals.repo.Create: %w", err)
	}
	return rec, nil
}

// GetByID fetches a single approval row scoped to the clinic.
func (r *Repository) GetByID(ctx context.Context, id, clinicID uuid.UUID) (*Record, error) {
	q := fmt.Sprintf(`SELECT %s FROM compliance_approvals
		WHERE id = $1 AND clinic_id = $2`, approvalCols)
	rec, err := scanApproval(r.db.QueryRow(ctx, q, id, clinicID))
	if err != nil {
		return nil, fmt.Errorf("approvals.repo.GetByID: %w", err)
	}
	return rec, nil
}

// Decide transitions pending → approved | challenged. Atomic: the WHERE
// clause guards against double-deciding a row.
func (r *Repository) Decide(ctx context.Context, p DecideParams) (*Record, error) {
	q := fmt.Sprintf(`
		UPDATE compliance_approvals
		SET status          = $3,
		    decided_by      = $4,
		    decided_at      = $5,
		    decided_comment = $6
		WHERE id = $1 AND clinic_id = $2 AND status = 'pending'
		RETURNING %s`, approvalCols)
	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, string(p.NewStatus),
		p.DecidedBy, p.DecidedAt, p.DecidedComment,
	)
	rec, err := scanApproval(row)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// UPDATE matched 0 rows — either missing or already decided.
			// Differentiate by a secondary lookup so callers map cleanly
			// to 404 vs 409.
			var status string
			lookup := r.db.QueryRow(ctx,
				`SELECT status FROM compliance_approvals WHERE id = $1 AND clinic_id = $2`,
				p.ID, p.ClinicID,
			).Scan(&status)
			if errors.Is(lookup, pgx.ErrNoRows) {
				return nil, domain.ErrNotFound
			}
			if lookup != nil {
				return nil, fmt.Errorf("approvals.repo.Decide: status check: %w", lookup)
			}
			return nil, domain.ErrConflict
		}
		return nil, fmt.Errorf("approvals.repo.Decide: %w", err)
	}
	return rec, nil
}

// ListPending returns the queue rows for a clinic, ordered by deadline
// ascending so overdue and soon-due rows surface first. By default
// excludes rows the requesting staff submitted (can't approve your
// own); when OnlyOwnSubmitter=true the filter inverts to ONLY return
// the caller's own submissions (FE "Submitted by you" tab).
func (r *Repository) ListPending(ctx context.Context, p ListPendingParams) ([]*Record, error) {
	args := []any{p.ClinicID, p.ExcludeSubmitter}
	op := "<>"
	if p.OnlyOwnSubmitter {
		op = "="
	}
	where := fmt.Sprintf("clinic_id = $1 AND status = 'pending' AND submitted_by %s $2", op)
	if p.EntityKind != nil {
		args = append(args, string(*p.EntityKind))
		where += fmt.Sprintf(" AND entity_kind = $%d", len(args))
	}
	limit := p.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	q := fmt.Sprintf(`SELECT %s FROM compliance_approvals
		WHERE %s ORDER BY deadline_at ASC LIMIT $%d`,
		approvalCols, where, len(args))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("approvals.repo.ListPending: %w", err)
	}
	defer rows.Close()

	var out []*Record
	for rows.Next() {
		rec, err := scanApproval(rows)
		if err != nil {
			return nil, fmt.Errorf("approvals.repo.ListPending: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("approvals.repo.ListPending: rows: %w", err)
	}
	return out, nil
}

// GetLatestForEntity returns the most recent approval row for an entity
// regardless of status. Used to render the audit trail on the entity.
func (r *Repository) GetLatestForEntity(ctx context.Context, kind domain.ApprovalEntityKind, entityID uuid.UUID) (*Record, error) {
	q := fmt.Sprintf(`SELECT %s FROM compliance_approvals
		WHERE entity_kind = $1 AND entity_id = $2
		ORDER BY created_at DESC LIMIT 1`, approvalCols)
	rec, err := scanApproval(r.db.QueryRow(ctx, q, string(kind), entityID))
	if err != nil {
		return nil, fmt.Errorf("approvals.repo.GetLatestForEntity: %w", err)
	}
	return rec, nil
}

// ListPendingForSubject — pending rows scoped to one subject. Used by
// the subject-hub "Pending compliance" card. The unique-pending index
// ensures at most one pending row per (kind, entity); ordering by
// deadline puts the most urgent on top.
func (r *Repository) ListPendingForSubject(ctx context.Context, clinicID, subjectID uuid.UUID, limit int) ([]*Record, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := fmt.Sprintf(`SELECT %s FROM compliance_approvals
		WHERE clinic_id = $1 AND subject_id = $2 AND status = 'pending'
		ORDER BY deadline_at ASC LIMIT $3`, approvalCols)
	rows, err := r.db.Query(ctx, q, clinicID, subjectID, limit)
	if err != nil {
		return nil, fmt.Errorf("approvals.repo.ListPendingForSubject: %w", err)
	}
	defer rows.Close()
	var out []*Record
	for rows.Next() {
		rec, err := scanApproval(rows)
		if err != nil {
			return nil, fmt.Errorf("approvals.repo.ListPendingForSubject: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("approvals.repo.ListPendingForSubject: rows: %w", err)
	}
	return out, nil
}

// CountPendingForDecider — drives the "N approvals waiting" dashboard chip.
func (r *Repository) CountPendingForDecider(ctx context.Context, clinicID, staffID uuid.UUID) (int, error) {
	const q = `
		SELECT COUNT(*) FROM compliance_approvals
		WHERE clinic_id = $1 AND status = 'pending' AND submitted_by <> $2`
	var n int
	if err := r.db.QueryRow(ctx, q, clinicID, staffID).Scan(&n); err != nil {
		return 0, fmt.Errorf("approvals.repo.CountPendingForDecider: %w", err)
	}
	return n, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanApproval(row scannable) (*Record, error) {
	var r Record
	var entityKind, status string
	err := row.Scan(
		&r.ID, &r.ClinicID, &entityKind, &r.EntityID, &r.EntityOp, &status,
		&r.SubmittedBy, &r.SubmittedAt, &r.SubmittedNote, &r.DeadlineAt,
		&r.DecidedBy, &r.DecidedAt, &r.DecidedComment,
		&r.SubjectID, &r.NoteID, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("approvals.repo.scanApproval: %w", err)
	}
	r.EntityKind = domain.ApprovalEntityKind(entityKind)
	r.Status = domain.ApprovalStatus(status)
	return &r, nil
}
