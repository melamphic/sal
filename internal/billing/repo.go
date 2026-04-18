package billing

import "context"

// repo is the interface the billing Service depends on for data access.
// Only the stripe_events table is owned by billing — clinic mutations
// go through the ClinicUpdater adapter (see service.go).
type repo interface {
	RecordEvent(ctx context.Context, p RecordEventParams) error
}
