package billing

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// EventStatus labels the outcome of webhook processing. Persisted on
// stripe_events for ops audit.
const (
	EventStatusProcessed = "processed" // matched a clinic, state written
	EventStatusIgnored   = "ignored"   // event type not in dispatch table
	EventStatusUnmapped  = "unmapped"  // no clinic matched the customer id
)

// RecordEventParams is the idempotency-gate insert for every Stripe
// webhook. A duplicate event_id returns domain.ErrConflict — callers
// treat that as "already processed" and 200 back.
type RecordEventParams struct {
	EventID   string
	EventType string
	ClinicID  *uuid.UUID
	Status    string
}

// Repository owns the stripe_events table.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new billing Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// RecordEvent inserts a Stripe webhook event. Duplicate event_id returns
// domain.ErrConflict so the service can short-circuit to an idempotent 200.
func (r *Repository) RecordEvent(ctx context.Context, p RecordEventParams) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO stripe_events (event_id, event_type, clinic_id, status)
		VALUES ($1, $2, $3, $4)
	`, p.EventID, p.EventType, p.ClinicID, p.Status)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return domain.ErrConflict
		}
		return fmt.Errorf("billing.repo.RecordEvent: %w", err)
	}
	return nil
}
