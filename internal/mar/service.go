package mar

// Service scaffolding for the MAR module.
//
// Phase 3a: minimal Service struct + NewService so handlers compile and
// app wiring can construct it. Phase 3b fills in the real methods —
// scheduled-dose generator, admin-event recorder with CD cross-link,
// per-resident chain compute, witness rule enforcement.

// Service is the MAR business-logic layer. Concurrency-safe — share
// across handlers without locking.
type Service struct {
	repo repo
}

// NewService constructs the MAR service. Phase 3b adds dependencies on
// drugs.Service (LogOperationTx) + a clinic-state lookup.
func NewService(r repo) *Service {
	return &Service{repo: r}
}
