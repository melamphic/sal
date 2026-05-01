package drugs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/melamphic/sal/internal/domain"
	"github.com/riverqueue/river"
)

// PurgeRetentionExpiredArgs is the River periodic-job payload that
// drives the daily soft-delete sweep over drug_operations_log + (in
// follow-on phases) drug_reconciliation + mar_administration_events.
//
// Periodic registration lives in app.go alongside the report-schedule
// fire-loop; per design doc §5.5 the job runs daily at 03:00 (clinic
// local time is irrelevant — retention is per-row, not per-clinic).
type PurgeRetentionExpiredArgs struct{}

// Kind returns the unique job type string used by River.
func (PurgeRetentionExpiredArgs) Kind() string { return "drugs_purge_retention_expired" }

// PurgeRetentionExpiredWorker scans drug_operations_log for rows past
// retention_until and stamps archived_at. Idempotent — re-running the
// job is a no-op when nothing has expired since the last run.
//
// Why a worker (not a service method called from a handler): retention
// is a *system* concern, not a clinic-action. No HTTP callers, no
// per-clinic tenancy required. River drives it via PeriodicJobs.
type PurgeRetentionExpiredWorker struct {
	river.WorkerDefaults[PurgeRetentionExpiredArgs]
	repo repo
}

// NewPurgeRetentionExpiredWorker constructs the worker.
func NewPurgeRetentionExpiredWorker(r repo) *PurgeRetentionExpiredWorker {
	return &PurgeRetentionExpiredWorker{repo: r}
}

// Work runs one purge cycle. Logs the count for ops visibility +
// telemetry. Errors propagate to River for retry.
func (w *PurgeRetentionExpiredWorker) Work(ctx context.Context, _ *river.Job[PurgeRetentionExpiredArgs]) error {
	asOf := domain.TimeNow()
	n, err := w.repo.SoftDeleteOpsPastRetention(ctx, asOf)
	if err != nil {
		return fmt.Errorf("drugs.jobs.PurgeRetentionExpired: %w", err)
	}
	slog.Info("drugs.purge_retention_expired",
		"as_of", asOf.Format("2006-01-02"),
		"rows_archived", n,
	)
	return nil
}
