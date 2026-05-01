package drugs

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all drug endpoints on the router. Permission gating uses
// the existing domain.Permissions struct (Dispense / ManagePatients /
// ViewAllPatients / GenerateAuditExport) until granular drug-specific
// permissions are wired into the JWT path. See the drugs README for the
// permission matrix and the planned migration to per-action perms.
func (h *Handler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	manageShelf := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManagePatients })
	dispense := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.Dispense })
	viewPatients := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ViewAllPatients || p.ViewOwnPatients })
	reconcile := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.GenerateAuditExport })
	security := []map[string][]string{{"bearerAuth": {}}}

	// ── Catalog (read-only reference data, any authenticated staff) ──────

	huma.Register(api, huma.Operation{
		OperationID: "list-drug-catalog",
		Method:      http.MethodGet,
		Path:        "/api/v1/drugs/catalog",
		Summary:     "List drug catalog for the clinic's vertical and country",
		Description: "Returns the system master catalog merged with any clinic-specific override drugs. Vertical and country are read from the clinic record; the catalog auto-scopes to that combination.",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.listCatalog)

	huma.Register(api, huma.Operation{
		OperationID: "get-drug-catalog-entry",
		Method:      http.MethodGet,
		Path:        "/api/v1/drugs/catalog/{entry_id}",
		Summary:     "Get a single drug catalog entry",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.getCatalogEntry)

	// ── Override drugs (clinic-defined custom entries) ───────────────────

	huma.Register(api, huma.Operation{
		OperationID:   "create-override-drug",
		Method:        http.MethodPost,
		Path:          "/api/v1/drugs/overrides",
		Summary:       "Create a clinic-specific override drug",
		Description:   "Use this for compounded products or locally registered drugs not in the system master catalog. Requires manage-shelf permission.",
		Tags:          []string{"Drugs"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, manageShelf},
		DefaultStatus: http.StatusCreated,
	}, h.createOverrideDrug)

	huma.Register(api, huma.Operation{
		OperationID: "update-override-drug",
		Method:      http.MethodPatch,
		Path:        "/api/v1/drugs/overrides/{id}",
		Summary:     "Update an override drug's metadata",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageShelf},
	}, h.updateOverrideDrug)

	huma.Register(api, huma.Operation{
		OperationID: "archive-override-drug",
		Method:      http.MethodDelete,
		Path:        "/api/v1/drugs/overrides/{id}",
		Summary:     "Archive (soft-delete) an override drug",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageShelf},
	}, h.archiveOverrideDrug)

	// ── Shelf (per-clinic inventory) ─────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID:   "create-drug-shelf-entry",
		Method:        http.MethodPost,
		Path:          "/api/v1/drugs/shelf",
		Summary:       "Add (drug × strength × batch × location) to the shelf",
		Description:   "Records receipt of new stock. Logs an opening 'receive' operation in the same transaction so the ledger is complete from the start.",
		Tags:          []string{"Drugs"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, manageShelf},
		DefaultStatus: http.StatusCreated,
	}, h.createShelf)

	huma.Register(api, huma.Operation{
		OperationID: "list-drug-shelf",
		Method:      http.MethodGet,
		Path:        "/api/v1/drugs/shelf",
		Summary:     "List shelf entries (paginated, filterable)",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageShelf},
	}, h.listShelf)

	huma.Register(api, huma.Operation{
		OperationID: "get-drug-shelf-entry",
		Method:      http.MethodGet,
		Path:        "/api/v1/drugs/shelf/{id}",
		Summary:     "Get a shelf entry",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageShelf},
	}, h.getShelf)

	huma.Register(api, huma.Operation{
		OperationID: "update-drug-shelf-meta",
		Method:      http.MethodPatch,
		Path:        "/api/v1/drugs/shelf/{id}",
		Summary:     "Update shelf entry metadata (location, par level, notes)",
		Description: "Balance changes happen via /drugs/operations only — this endpoint cannot adjust balance.",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageShelf},
	}, h.updateShelfMeta)

	huma.Register(api, huma.Operation{
		OperationID: "archive-drug-shelf-entry",
		Method:      http.MethodDelete,
		Path:        "/api/v1/drugs/shelf/{id}",
		Summary:     "Archive (soft-delete) a shelf entry",
		Description: "Existing operations log rows referencing this shelf remain valid.",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageShelf},
	}, h.archiveShelf)

	// ── Operations (append-only ledger) ──────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID:   "log-drug-operation",
		Method:        http.MethodPost,
		Path:          "/api/v1/drugs/operations",
		Summary:       "Log a drug operation (administer/dispense/discard/receive/transfer/adjust)",
		Description:   "Atomically appends to the ledger and updates the shelf balance. Witness staff_id is required for controlled drugs and must differ from the administering staff. The operation's permission requirement depends on the drug's controls metadata (controlled drugs require Dispense + a witness with the same perm).",
		Tags:          []string{"Drugs"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, dispense},
		DefaultStatus: http.StatusCreated,
	}, h.logOperation)

	huma.Register(api, huma.Operation{
		OperationID: "list-drug-operations",
		Method:      http.MethodGet,
		Path:        "/api/v1/drugs/operations",
		Summary:     "List drug operations (paginated, filterable)",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, viewPatients},
	}, h.listOperations)

	huma.Register(api, huma.Operation{
		OperationID: "get-drug-operation",
		Method:      http.MethodGet,
		Path:        "/api/v1/drugs/operations/{id}",
		Summary:     "Get one drug operation",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, viewPatients},
	}, h.getOperation)

	huma.Register(api, huma.Operation{
		OperationID: "confirm-drug-operation",
		Method:      http.MethodPost,
		Path:        "/api/v1/drugs/operations/{id}/confirm",
		Summary:     "Confirm a pending drug operation",
		Description: "system.drug_op widgets create rows in 'pending_confirm' until the clinician explicitly confirms — note submission is blocked while any drug op linked to the note is unconfirmed. This endpoint is idempotent: re-calling on a confirmed row returns it unchanged.",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, dispense},
	}, h.confirmOperation)

	huma.Register(api, huma.Operation{
		OperationID: "list-subject-drug-history",
		Method:      http.MethodGet,
		Path:        "/api/v1/drugs/subjects/{subject_id}/medications",
		Summary:     "List a subject's drug history",
		Description: "Filters operations by subject. Each call is logged to subject_access_log for compliance traceability.",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, viewPatients},
	}, h.listSubjectMedications)

	// ── Reconciliation (period-close) ────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID:   "start-drug-reconciliation",
		Method:        http.MethodPost,
		Path:          "/api/v1/drugs/reconciliations",
		Summary:       "Start a reconciliation for (shelf, period) with primary staff signoff",
		Description:   "Computes ledger_count from the operations log, persists physical_count + diff, and locks all in-period operations to this reconciliation. Status is 'clean' if equal, 'discrepancy_logged' otherwise. Two-staff signoff completes via /sign-secondary.",
		Tags:          []string{"Drugs"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, reconcile},
		DefaultStatus: http.StatusCreated,
	}, h.startReconciliation)

	huma.Register(api, huma.Operation{
		OperationID: "sign-secondary-drug-reconciliation",
		Method:      http.MethodPost,
		Path:        "/api/v1/drugs/reconciliations/{id}/sign-secondary",
		Summary:     "Secondary staff signs a reconciliation",
		Description: "Required for controlled-drug reconciliations under VCNZ + analogous regulators. Secondary must differ from primary.",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, reconcile},
	}, h.signSecondaryReconciliation)

	huma.Register(api, huma.Operation{
		OperationID: "report-drug-reconciliation-to-regulator",
		Method:      http.MethodPost,
		Path:        "/api/v1/drugs/reconciliations/{id}/report",
		Summary:     "Mark a discrepancy reconciliation as reported to regulator",
		Description: "Privacy-officer flow. Records the reported_at + reported_by; the row stays in the ledger.",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, reconcile},
	}, h.reportReconciliation)

	huma.Register(api, huma.Operation{
		OperationID: "list-drug-reconciliations",
		Method:      http.MethodGet,
		Path:        "/api/v1/drugs/reconciliations",
		Summary:     "List reconciliations (paginated, filterable)",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, reconcile},
	}, h.listReconciliations)

	huma.Register(api, huma.Operation{
		OperationID: "get-drug-reconciliation",
		Method:      http.MethodGet,
		Path:        "/api/v1/drugs/reconciliations/{id}",
		Summary:     "Get one reconciliation",
		Tags:        []string{"Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, reconcile},
	}, h.getReconciliation)
}
