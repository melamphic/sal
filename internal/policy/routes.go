package policy

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all policy routes on the router.
// All policy endpoints require manage_policies permission.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	managePolicies := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManagePolicies
	})
	rollbackPolicies := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.RollbackPolicies
	})
	security := []map[string][]string{{"bearerAuth": {}}}

	// ── Folders ───────────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID:   "create-policy-folder",
		Method:        http.MethodPost,
		Path:          "/api/v1/policy-folders",
		Summary:       "Create policy folder",
		Description:   "Creates a single-level folder for organising policies within a clinic. Requires manage_policies permission.",
		Tags:          []string{"Policy"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, managePolicies},
		DefaultStatus: http.StatusCreated,
	}, h.createFolder)

	huma.Register(api, huma.Operation{
		OperationID: "list-policy-folders",
		Method:      http.MethodGet,
		Path:        "/api/v1/policy-folders",
		Summary:     "List policy folders",
		Description: "Returns all policy folders for the clinic. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.listFolders)

	huma.Register(api, huma.Operation{
		OperationID: "update-policy-folder",
		Method:      http.MethodPut,
		Path:        "/api/v1/policy-folders/{folder_id}",
		Summary:     "Update policy folder",
		Description: "Renames a policy folder. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.updateFolder)

	// ── Policies ──────────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID:   "create-policy",
		Method:        http.MethodPost,
		Path:          "/api/v1/policies",
		Summary:       "Create policy",
		Description:   "Creates a new policy with an empty draft version. Requires manage_policies permission.",
		Tags:          []string{"Policy"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, managePolicies},
		DefaultStatus: http.StatusCreated,
	}, h.createPolicy)

	huma.Register(api, huma.Operation{
		OperationID: "list-policies",
		Method:      http.MethodGet,
		Path:        "/api/v1/policies",
		Summary:     "List policies",
		Description: "Returns a paginated list of policies. Optionally filter by folder or include archived. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.listPolicies)

	huma.Register(api, huma.Operation{
		OperationID: "get-policy",
		Method:      http.MethodGet,
		Path:        "/api/v1/policies/{policy_id}",
		Summary:     "Get policy",
		Description: "Returns a policy with its current draft (if any) and latest published version. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.getPolicy)

	huma.Register(api, huma.Operation{
		OperationID: "update-policy-draft",
		Method:      http.MethodPut,
		Path:        "/api/v1/policies/{policy_id}/draft",
		Summary:     "Update policy draft",
		Description: "Updates the draft version content and policy metadata. Creates a new draft if none exists. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.updateDraft)

	huma.Register(api, huma.Operation{
		OperationID:   "publish-policy",
		Method:        http.MethodPost,
		Path:          "/api/v1/policies/{policy_id}/publish",
		Summary:       "Publish policy",
		Description:   "Freezes the current draft and assigns a semver version number. Requires manage_policies permission.",
		Tags:          []string{"Policy"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, managePolicies},
		DefaultStatus: http.StatusCreated,
	}, h.publishPolicy)

	huma.Register(api, huma.Operation{
		OperationID: "discard-policy-draft",
		Method:      http.MethodDelete,
		Path:        "/api/v1/policies/{policy_id}/draft",
		Summary:     "Discard policy draft",
		Description: "Deletes the current draft version of a policy. The latest published version (if any) remains active. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.discardDraft)

	huma.Register(api, huma.Operation{
		OperationID:   "rollback-policy",
		Method:        http.MethodPost,
		Path:          "/api/v1/policies/{policy_id}/rollback",
		Summary:       "Rollback policy",
		Description:   "Creates a new draft by copying content from a prior published version. Discard any existing draft before rolling back. Requires rollback_policies permission.",
		Tags:          []string{"Policy"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, rollbackPolicies},
		DefaultStatus: http.StatusCreated,
	}, h.rollbackPolicy)

	huma.Register(api, huma.Operation{
		OperationID: "retire-policy",
		Method:      http.MethodPost,
		Path:        "/api/v1/policies/{policy_id}/retire",
		Summary:     "Retire policy",
		Description: "Archives the policy so it can no longer be edited or published. Automatically removes links from all forms that reference it. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.retirePolicy)

	// ── Versions ──────────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "list-policy-versions",
		Method:      http.MethodGet,
		Path:        "/api/v1/policies/{policy_id}/versions",
		Summary:     "List policy versions",
		Description: "Returns published version history for a policy, newest first. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.listVersions)

	huma.Register(api, huma.Operation{
		OperationID: "get-policy-version",
		Method:      http.MethodGet,
		Path:        "/api/v1/policies/{policy_id}/versions/{version_id}",
		Summary:     "Get policy version",
		Description: "Returns a specific policy version including its full content. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.getVersion)

	// ── Clauses ───────────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "upsert-policy-clauses",
		Method:      http.MethodPut,
		Path:        "/api/v1/policies/{policy_id}/versions/{version_id}/clauses",
		Summary:     "Replace policy clauses",
		Description: "Atomically replaces all enforceable clauses for a policy version. Each clause references a block_id from the content and carries a parity level (high/medium/low). Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.upsertClauses)

	huma.Register(api, huma.Operation{
		OperationID: "list-policy-clauses",
		Method:      http.MethodGet,
		Path:        "/api/v1/policies/{policy_id}/versions/{version_id}/clauses",
		Summary:     "List policy clauses",
		Description: "Returns all enforceable clauses for a policy version. Requires manage_policies permission.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePolicies},
	}, h.listClauses)
}
