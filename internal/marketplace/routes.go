package marketplace

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers marketplace routes.
//
// All marketplace routes are authenticated — the marketplace lives inside the
// post-login UI only. Vertical scoping is enforced in the service layer.
//
//   - Any authenticated staff: browse listings, read reviews, see own notifications
//   - `perm_marketplace_download`: acquire/purchase/import/review
//   - `perm_marketplace_manage`:  register publisher, create/publish listings,
//     Stripe onboarding, grant/revoke badges (authority gate inside service)
//
// The Stripe webhook is unauthenticated (verified by signature).
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	canDownload := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.MarketplaceDownload })
	canManage := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.MarketplaceManage })
	security := []map[string][]string{{"bearerAuth": {}}}

	// ── Any authenticated staff ──────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "list-marketplace-listings",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/listings",
		Summary:     "Browse marketplace listings",
		Description: "Published listings auto-scoped to the caller's clinic vertical. Salvia platform publisher can browse cross-vertical.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.listListings)

	huma.Register(api, huma.Operation{
		OperationID: "get-marketplace-listing",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/listings/{slug}",
		Summary:     "Get a listing by slug",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.getListingBySlug)

	huma.Register(api, huma.Operation{
		OperationID: "get-marketplace-version",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/listings/{listing_id}/versions/{version_id}",
		Summary:     "Get a specific version of a listing",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.getVersion)

	huma.Register(api, huma.Operation{
		OperationID: "list-marketplace-reviews",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/listings/{listing_id}/reviews",
		Summary:     "List reviews for a listing",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.listReviews)

	huma.Register(api, huma.Operation{
		OperationID: "list-my-marketplace-notifications",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/my/notifications",
		Summary:     "List unread marketplace upgrade notifications",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.listMyNotifications)

	huma.Register(api, huma.Operation{
		OperationID: "mark-marketplace-notification-seen",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/my/notifications/{notification_id}/seen",
		Summary:     "Mark an upgrade notification as seen",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.markNotificationSeen)

	// ── MarketplaceDownload (acquire/import/review/purchase) ─────────────────

	huma.Register(api, huma.Operation{
		OperationID: "acquire-marketplace-listing",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/listings/{listing_id}/acquire",
		Summary:     "Acquire a free marketplace listing",
		Description: "Rejects paid listings — use /purchase for those.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canDownload},
	}, h.acquireListing)

	huma.Register(api, huma.Operation{
		OperationID: "purchase-marketplace-listing",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/listings/{listing_id}/purchase",
		Summary:     "Create a Stripe PaymentIntent for a paid listing",
		Description: "Returns the client_secret; the client confirms payment via Stripe SDK. Blocked for trial clinics.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canDownload},
	}, h.purchaseListing)

	huma.Register(api, huma.Operation{
		OperationID: "import-marketplace-acquisition",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/acquisitions/{acquisition_id}/import",
		Summary:     "Import an acquired listing into clinic forms",
		Description: "Materialises a marketplace package into a fresh tenant form. Opt-in policy bundling; accepted_policy_attribution must be true when include_policies=true. Pass version_id to import a SPECIFIC published version — used by the upgrade flow when a newer version becomes available; the new version lands as a separate tenant form so the buyer can compare side-by-side without disturbing the existing one. Importing automatically dismisses any matching upgrade notification.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canDownload},
	}, h.importAcquisition)

	huma.Register(api, huma.Operation{
		OperationID: "list-my-marketplace-acquisitions",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/my/acquisitions",
		Summary:     "List my clinic's acquisitions",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canDownload},
	}, h.listMyAcquisitions)

	huma.Register(api, huma.Operation{
		OperationID: "create-marketplace-review",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/acquisitions/{acquisition_id}/reviews",
		Summary:     "Submit a review for an acquired listing",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canDownload},
	}, h.createReview)

	// ── MarketplaceManage (publisher + badges) ───────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "register-marketplace-publisher",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/publishers",
		Summary:     "Self-register as a publisher",
		Description: "Creates a publisher_accounts row for the caller's clinic with status='active'. Idempotent. Trial clinics blocked.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.registerPublisher)

	huma.Register(api, huma.Operation{
		OperationID: "create-marketplace-listing",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/listings",
		Summary:     "Create a marketplace listing (draft)",
		Description: "Caller's clinic must own the publisher. bundle_type defaults to 'bundled'.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.createListing)

	huma.Register(api, huma.Operation{
		OperationID: "publish-marketplace-listing-version",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/listings/{listing_id}/versions",
		Summary:     "Publish a new listing version from a tenant form",
		Description: "Snapshots the caller's form latest published version. Bundles policies when bundle_type='bundled'.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.publishVersion)

	huma.Register(api, huma.Operation{
		OperationID: "publish-marketplace-listing",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/listings/{listing_id}/publish",
		Summary:     "Transition a listing from draft to published",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.publishListing)

	huma.Register(api, huma.Operation{
		OperationID: "list-my-publisher-listings",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/my/listings",
		Summary:     "List my publisher's listings",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.listMyPublisherListings)

	huma.Register(api, huma.Operation{
		OperationID: "start-publisher-stripe-onboarding",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/publishers/{publisher_id}/stripe-onboarding",
		Summary:     "Start Stripe Connect Express onboarding",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.startOnboarding)

	// ── Badge grants + suspend (authority gates inside service) ──────────────

	huma.Register(api, huma.Operation{
		OperationID: "grant-publisher-badge",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/publishers/{publisher_id}/badge",
		Summary:     "Grant verified_badge and/or authority_type to a publisher",
		Description: "Salvia can grant anything. Authority grantors can grant verified_badge within their own vertical only.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.grantBadge)

	huma.Register(api, huma.Operation{
		OperationID: "revoke-publisher-badge",
		Method:      http.MethodDelete,
		Path:        "/api/v1/marketplace/publishers/{publisher_id}/badge",
		Summary:     "Revoke a publisher's badge and authority",
		Description: "Grantor may revoke own grants; Salvia may revoke any.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.revokeBadge)

	huma.Register(api, huma.Operation{
		OperationID: "suspend-marketplace-listing",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/listings/{listing_id}/suspend",
		Summary:     "Suspend a listing (Salvia only)",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.suspendListing)

	huma.Register(api, huma.Operation{
		OperationID: "update-marketplace-listing",
		Method:      http.MethodPatch,
		Path:        "/api/v1/marketplace/listings/{listing_id}",
		Summary:     "Update listing metadata (publisher self-serve)",
		Description: "Partial update — only non-null body fields are applied. Draft listings allow any field; published listings reject changes to name / pricing / bundle_type (those require archive + relist or Salvia moderation).",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.updateListing)

	huma.Register(api, huma.Operation{
		OperationID: "archive-marketplace-listing",
		Method:      http.MethodPost,
		Path:        "/api/v1/marketplace/listings/{listing_id}/archive",
		Summary:     "Archive a listing (publisher self-serve)",
		Description: "Sets status to 'archived'. Existing acquisitions remain valid; new acquire/purchase requests reject. Idempotent. Suspended listings cannot be archived (Salvia must lift the suspension first).",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.archiveListing)

	huma.Register(api, huma.Operation{
		OperationID: "delete-marketplace-listing",
		Method:      http.MethodDelete,
		Path:        "/api/v1/marketplace/listings/{listing_id}",
		Summary:     "Delete a draft listing",
		Description: "Hard-deletes a listing along with any unpublished version rows. Only valid when status='draft'; published listings must be archived instead so historical acquisitions still resolve.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.deleteListing)

	huma.Register(api, huma.Operation{
		OperationID: "list-marketplace-pack-forms",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/listings/{listing_id}/pack-forms",
		Summary:     "List the source forms composing a pack listing",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.listPackForms)

	huma.Register(api, huma.Operation{
		OperationID: "set-marketplace-pack-forms",
		Method:      http.MethodPut,
		Path:        "/api/v1/marketplace/listings/{listing_id}/pack-forms",
		Summary:     "Replace the source-form composition of a pack listing",
		Description: "Atomic replace — the body's ordered array of tenant form UUIDs becomes the new pack composition. Only valid while the listing is a draft (composition is locked once published so existing buyers don't see surprise changes).",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.setPackForms)

	huma.Register(api, huma.Operation{
		OperationID: "list-my-marketplace-earnings",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/my/earnings",
		Summary:     "List paid acquisitions for my publisher",
		Description: "Returns paid acquisitions (active + refunded) with platform fees and net publisher cut, most-recent first. Free acquisitions excluded — they never carry money.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.listMyEarnings)

	huma.Register(api, huma.Operation{
		OperationID: "my-marketplace-earnings-summary",
		Method:      http.MethodGet,
		Path:        "/api/v1/marketplace/my/earnings/summary",
		Summary:     "Monthly earnings summary",
		Description: "Up to 36 months of bucketed gross/fee/net + order/refund counts for the caller's publisher. Default window is 12 months.",
		Tags:        []string{"Marketplace"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, canManage},
	}, h.myEarningsSummary)

	// ── Stripe webhook (raw Chi, no auth — signature-verified) ───────────────

	r.Post("/api/v1/marketplace/webhooks/stripe", h.StripeWebhookHandler())
}
