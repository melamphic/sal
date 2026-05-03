package mar

import "github.com/danielgtaylor/huma/v2"

// Routes wires MAR endpoints onto a huma API instance. Phase 3a is a
// stub — Phase 3c registers prescriptions / scheduled-doses / rounds /
// administration-events endpoints per design doc §6.2.
func (h *Handler) Routes(_ huma.API) {
	// Phase 3c will register:
	//   POST   /api/v1/mar/prescriptions
	//   GET    /api/v1/mar/prescriptions/{resident_id}
	//   PATCH  /api/v1/mar/prescriptions/{id}
	//   POST   /api/v1/mar/prescriptions/{id}/discontinue
	//   GET    /api/v1/mar/scheduled-doses
	//   POST   /api/v1/mar/rounds
	//   POST   /api/v1/mar/rounds/{id}/complete
	//   POST   /api/v1/mar/administration-events
	//   GET    /api/v1/mar/administration-events
	//   POST   /api/v1/mar/administration-events/{id}/correct
}
