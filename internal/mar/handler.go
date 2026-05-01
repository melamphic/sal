package mar

// Handler scaffolding for the MAR module.
//
// Phase 3a: minimal Handler struct so app wiring compiles. Phase 3c
// implements the actual huma routes (POST /mar/prescriptions, GET
// /mar/prescriptions/{resident_id}, POST /mar/administration-events,
// etc.) per design doc §6.2.

// Handler exposes MAR endpoints over the huma router.
type Handler struct {
	svc *Service
}

// NewHandler constructs the MAR handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}
