package forms

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all form-related routes onto the provided Chi router.
// All routes require a valid JWT (enforced by AuthenticateHuma).
// Form management operations require ManageForms permission.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	manageForms := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManageForms })
	security := []map[string][]string{{"bearerAuth": {}}}

	// ── Form groups ───────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "create-form-group",
		Method:      http.MethodPost,
		Path:        "/api/v1/form-groups",
		Summary:     "Create a form group",
		Description: "Creates a top-level folder for organising forms. Only one level of grouping is supported.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.createGroup)

	huma.Register(api, huma.Operation{
		OperationID: "list-form-groups",
		Method:      http.MethodGet,
		Path:        "/api/v1/form-groups",
		Summary:     "List form groups",
		Description: "Returns all form groups for the authenticated clinic, ordered alphabetically.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.listGroups)

	huma.Register(api, huma.Operation{
		OperationID: "update-form-group",
		Method:      http.MethodPatch,
		Path:        "/api/v1/form-groups/{group_id}",
		Summary:     "Update a form group",
		Description: "Updates the name and description of a form group.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.updateGroup)

	// ── Forms ─────────────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "create-form",
		Method:      http.MethodPost,
		Path:        "/api/v1/forms",
		Summary:     "Create a form",
		Description: "Creates a new form with an empty draft version. Use PUT /forms/{id}/draft to add fields and publish when ready.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.createForm)

	huma.Register(api, huma.Operation{
		OperationID: "list-forms",
		Method:      http.MethodGet,
		Path:        "/api/v1/forms",
		Summary:     "List forms",
		Description: "Returns a paginated list of forms for the clinic. Excludes retired forms by default; use include_archived=true to include them.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.listForms)

	huma.Register(api, huma.Operation{
		OperationID: "get-form",
		Method:      http.MethodGet,
		Path:        "/api/v1/forms/{form_id}",
		Summary:     "Get a form",
		Description: "Returns the form with its current draft (if any) and latest published version (if any), including all fields.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.getForm)

	huma.Register(api, huma.Operation{
		OperationID: "update-form-draft",
		Method:      http.MethodPut,
		Path:        "/api/v1/forms/{form_id}/draft",
		Summary:     "Update form draft",
		Description: "Replaces all fields on the current draft and updates form metadata. If no draft exists, one is created automatically. This is a full replacement — send the complete field list every time.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.updateDraft)

	huma.Register(api, huma.Operation{
		OperationID: "publish-form",
		Method:      http.MethodPost,
		Path:        "/api/v1/forms/{form_id}/publish",
		Summary:     "Publish form",
		Description: "Freezes the current draft and assigns a semver version number. Specify change_type=major for structural changes (fields added/removed/retyped) or minor for metadata-only changes.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.publishForm)

	huma.Register(api, huma.Operation{
		OperationID: "form-policy-check",
		Method:      http.MethodPost,
		Path:        "/api/v1/forms/{form_id}/policy-check",
		Summary:     "Run policy compliance check",
		Description: "Runs an AI policy compliance check against the linked policies and saves the result on the draft. The form must have at least one policy linked. Full LLM-based checking will be available once the policy engine is complete.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.policyCheckForm)

	huma.Register(api, huma.Operation{
		OperationID: "rollback-form",
		Method:      http.MethodPost,
		Path:        "/api/v1/forms/{form_id}/rollback",
		Summary:     "Rollback form to a prior version",
		Description: "Creates a new draft by copying all fields from a previously published version. Any existing draft must be discarded first. Publish the resulting draft to make the rollback live.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.rollbackForm)

	huma.Register(api, huma.Operation{
		OperationID: "retire-form",
		Method:      http.MethodPost,
		Path:        "/api/v1/forms/{form_id}/retire",
		Summary:     "Retire a form",
		Description: "Removes the form from active use. In-flight notes that are currently processing will complete normally and will be tagged 'before decommission'. The form remains visible in history with include_archived=true.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.retireForm)

	huma.Register(api, huma.Operation{
		OperationID: "list-form-versions",
		Method:      http.MethodGet,
		Path:        "/api/v1/forms/{form_id}/versions",
		Summary:     "List form version history",
		Description: "Returns all published versions for a form in descending order (newest first). Includes change summaries and audit information.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.listVersions)

	// ── Policies ──────────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "list-form-policies",
		Method:      http.MethodGet,
		Path:        "/api/v1/forms/{form_id}/policies",
		Summary:     "List linked policies",
		Description: "Returns the UUIDs of all policies linked to this form.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.listLinkedPolicies)

	huma.Register(api, huma.Operation{
		OperationID: "link-form-policy",
		Method:      http.MethodPost,
		Path:        "/api/v1/forms/{form_id}/policies",
		Summary:     "Link a policy to a form",
		Description: "Associates a policy with a form. The policy ID is stored without FK validation until the policy engine is built.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.linkPolicy)

	huma.Register(api, huma.Operation{
		OperationID: "unlink-form-policy",
		Method:      http.MethodDelete,
		Path:        "/api/v1/forms/{form_id}/policies/{policy_id}",
		Summary:     "Unlink a policy from a form",
		Description: "Removes the association between a form and a policy.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.unlinkPolicy)

	// ── Style ─────────────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "get-form-style",
		Method:      http.MethodGet,
		Path:        "/api/v1/clinic/form-style",
		Summary:     "Get PDF style settings",
		Description: "Returns the clinic's current PDF export style (logo, colours, fonts, header/footer). Returns null body if no style has been configured.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.getStyle)

	huma.Register(api, huma.Operation{
		OperationID: "update-form-style",
		Method:      http.MethodPut,
		Path:        "/api/v1/clinic/form-style",
		Summary:     "Update PDF style settings",
		Description: "Saves a new version of the clinic's PDF style settings. All forms use the latest style version. Previous style versions are retained for audit purposes. Accepts either the flat fields (legacy simple mode) or the rich `config` JSON blob produced by the three-pane designer, or both — top-level values from `config` are mirrored into the flat columns.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.updateStyle)

	huma.Register(api, huma.Operation{
		OperationID: "list-form-style-versions",
		Method:      http.MethodGet,
		Path:        "/api/v1/clinic/form-style/versions",
		Summary:     "List PDF style version history",
		Description: "Returns every saved style version for the clinic, newest first. Used by the designer's version-history tab for diff/rollback UI.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.listStyleVersions)

	huma.Register(api, huma.Operation{
		OperationID: "list-form-style-presets",
		Method:      http.MethodGet,
		Path:        "/api/v1/clinic/form-style/presets",
		Summary:     "List starter themes for a vertical",
		Description: "Returns the curated starter themes (3 per vertical) the designer shows during onboarding and in Settings → Doc Theme. Pass ?vertical= to scope the result; unknown verticals return the 'general' set.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.listStylePresets)

	huma.Register(api, huma.Operation{
		OperationID: "upload-form-style-logo",
		Method:      http.MethodPost,
		Path:        "/api/v1/clinic/form-style/logo",
		Summary:     "Upload doc-theme logo",
		Description: "Uploads an image (PNG / JPEG / SVG / WEBP, max 4 MiB) to object storage for use as the doc-theme header logo. Returns the persisted key and a short-lived signed preview URL. Persist the key into config.header.logo_key via PUT /clinic/form-style to save the choice permanently.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.uploadStyleLogo)
}
