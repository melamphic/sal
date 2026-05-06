package marketplace

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires marketplace HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new marketplace Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Shared input types ────────────────────────────────────────────────────────

type paginationInput struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"100" default:"20" doc:"Number of results per page."`
	Offset int `query:"offset" minimum:"0" default:"0"   doc:"Number of results to skip."`
}

// ── Browse listings (public) ──────────────────────────────────────────────────

type listListingsInput struct {
	paginationInput
	Query        string `query:"q"              doc:"Free-text search across name, tags, descriptions."`
	Vertical     string `query:"vertical"       enum:"veterinary,dental,aged_care" doc:"Filter by clinic vertical."`
	PricingType  string `query:"pricing_type"   enum:"free,paid" doc:"Filter by free or paid."`
	VerifiedOnly bool   `query:"verified_only"  doc:"Only return listings from verified publishers."`
	PolicyLinked string `query:"policy_linked"  enum:"true,false" doc:"Filter listings that reference a policy (true) or do not (false)."`
	Sort         string `query:"sort"           enum:"relevance,rating,downloads,newest" default:"newest" doc:"Sort order."`
}

type listingListHTTPResponse struct {
	Body *ListingListResponse
}

func (h *Handler) listListings(ctx context.Context, input *listListingsInput) (*listingListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	svcInput := ListListingsInput{
		Sort:         input.Sort,
		Limit:        input.Limit,
		Offset:       input.Offset,
		VerifiedOnly: input.VerifiedOnly,
	}
	if input.Query != "" {
		svcInput.Query = &input.Query
	}
	if input.Vertical != "" {
		svcInput.Vertical = &input.Vertical
	}
	if input.PricingType != "" {
		svcInput.PricingType = &input.PricingType
	}
	switch input.PolicyLinked {
	case "true":
		v := true
		svcInput.PolicyLinked = &v
	case "false":
		v := false
		svcInput.PolicyLinked = &v
	}

	resp, err := h.svc.BrowseListings(ctx, clinicID, svcInput)
	if err != nil {
		return nil, mapError(err)
	}
	return &listingListHTTPResponse{Body: resp}, nil
}

// ── Get listing by slug (public) ──────────────────────────────────────────────

type getListingBySlugInput struct {
	Slug string `path:"slug" doc:"The listing's URL-safe slug."`
}

type listingWithVersionHTTPResponse struct {
	Body *struct {
		Listing       *ListingResponse `json:"listing"`
		LatestVersion *VersionResponse `json:"latest_version,omitempty"`
	}
}

func (h *Handler) getListingBySlug(ctx context.Context, input *getListingBySlugInput) (*listingWithVersionHTTPResponse, error) {
	listing, version, err := h.svc.GetListingBySlug(ctx, input.Slug)
	if err != nil {
		return nil, mapError(err)
	}
	resp := &listingWithVersionHTTPResponse{}
	resp.Body = &struct {
		Listing       *ListingResponse `json:"listing"`
		LatestVersion *VersionResponse `json:"latest_version,omitempty"`
	}{
		Listing:       listing,
		LatestVersion: version,
	}
	return resp, nil
}

// ── Get specific version (public) ─────────────────────────────────────────────

type getVersionInput struct {
	ListingID string `path:"listing_id" doc:"The listing's UUID."`
	VersionID string `path:"version_id" doc:"The version's UUID."`
}

type versionHTTPResponse struct {
	Body *VersionResponse
}

func (h *Handler) getVersion(ctx context.Context, input *getVersionInput) (*versionHTTPResponse, error) {
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	versionID, err := uuid.Parse(input.VersionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid version_id")
	}
	resp, err := h.svc.GetVersion(ctx, listingID, versionID)
	if err != nil {
		return nil, mapError(err)
	}
	return &versionHTTPResponse{Body: resp}, nil
}

// ── Acquire (authenticated) ───────────────────────────────────────────────────

type acquireInput struct {
	ListingID string `path:"listing_id" doc:"The listing's UUID."`
}

type acquisitionHTTPResponse struct {
	Body *AcquisitionResponse
}

func (h *Handler) acquireListing(ctx context.Context, input *acquireInput) (*acquisitionHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}

	resp, err := h.svc.Acquire(ctx, AcquireInput{
		ListingID: listingID,
		ClinicID:  clinicID,
		StaffID:   staffID,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &acquisitionHTTPResponse{Body: resp}, nil
}

// ── Import (authenticated) ────────────────────────────────────────────────────

type importInput struct {
	AcquisitionID string `path:"acquisition_id" doc:"The acquisition's UUID."`
	Body          struct {
		IncludePolicies           bool           `json:"include_policies" doc:"Import bundled policies alongside the form."`
		AcceptedPolicyAttribution bool           `json:"accepted_policy_attribution" doc:"Required true when IncludePolicies=true; records the license acknowledgement."`
		RelinkExistingPolicyIDs   map[int]string `json:"relink_existing_policy_ids,omitempty" doc:"Map of bundled policy index → existing local policy UUID. Used when IncludePolicies=false."`
		VersionID                 string         `json:"version_id,omitempty" doc:"Optional. UUID of a specific marketplace version to import. Use this to import a NEWER version into a fresh tenant form (the upgrade flow) — your existing imported form is left untouched. Must reference a published version of the acquired listing. When omitted, the acquisition's originally-pinned version is used (first-import flow)."`
	}
}

func (h *Handler) importAcquisition(ctx context.Context, input *importInput) (*acquisitionHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	acquisitionID, err := uuid.Parse(input.AcquisitionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid acquisition_id")
	}

	relink := make(map[int]uuid.UUID, len(input.Body.RelinkExistingPolicyIDs))
	for idx, id := range input.Body.RelinkExistingPolicyIDs {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid relink_existing_policy_ids value")
		}
		relink[idx] = parsed
	}

	var versionID *uuid.UUID
	if input.Body.VersionID != "" {
		parsed, err := uuid.Parse(input.Body.VersionID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid version_id")
		}
		versionID = &parsed
	}

	resp, err := h.svc.Import(ctx, ImportInput{
		AcquisitionID:             acquisitionID,
		ClinicID:                  clinicID,
		StaffID:                   staffID,
		IncludePolicies:           input.Body.IncludePolicies,
		AcceptedPolicyAttribution: input.Body.AcceptedPolicyAttribution,
		RelinkExistingPolicyIDs:   relink,
		VersionID:                 versionID,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &acquisitionHTTPResponse{Body: resp}, nil
}

// ── List my acquisitions (authenticated) ──────────────────────────────────────

type listMyAcquisitionsInput struct {
	paginationInput
}

type acquisitionListHTTPResponse struct {
	Body *AcquisitionListResponse
}

func (h *Handler) listMyAcquisitions(ctx context.Context, input *listMyAcquisitionsInput) (*acquisitionListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.ListMyAcquisitions(ctx, clinicID, input.Limit, input.Offset)
	if err != nil {
		return nil, mapError(err)
	}
	return &acquisitionListHTTPResponse{Body: resp}, nil
}

// ── Admin: create listing ─────────────────────────────────────────────────────
//
// Gated by perm_marketplace_manage. Phase 1 assumes the platform-admin
// clinic's super_admin is the only holder of this permission — see
// docs/marketplace.md §4.3. Multi-tenant enforcement of which clinic may
// publish comes in Phase 2 with proper admin identity.

type createListingBodyInput struct {
	Body struct {
		PublisherAccountID string   `json:"publisher_account_id" doc:"The publisher account the listing belongs to."`
		Vertical           string   `json:"vertical"             enum:"veterinary,dental,aged_care"`
		Name               string   `json:"name"                 minLength:"1"`
		Slug               string   `json:"slug"                 minLength:"1" maxLength:"200" pattern:"^[a-z0-9-]+$"`
		ShortDescription   string   `json:"short_description"    minLength:"1"`
		LongDescription    *string  `json:"long_description,omitempty"`
		Tags               []string `json:"tags,omitempty"`
		BundleType         string   `json:"bundle_type,omitempty" enum:"bundled,form_only" default:"bundled" doc:"'bundled' includes linked policies; 'form_only' omits them."`
		PricingType        string   `json:"pricing_type"         enum:"free,paid"`
		PriceCents         *int     `json:"price_cents,omitempty"`
		Currency           string   `json:"currency,omitempty"   default:"NZD"`
		PreviewFieldCount  int      `json:"preview_field_count,omitempty" minimum:"0" default:"3"`
	}
}

type listingHTTPResponse struct {
	Body *ListingResponse
}

func (h *Handler) createListing(ctx context.Context, input *createListingBodyInput) (*listingHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	publisherID, err := uuid.Parse(input.Body.PublisherAccountID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid publisher_account_id")
	}

	resp, err := h.svc.CreateListing(ctx, CreateListingInput{
		CallerClinicID:     clinicID,
		CallerStaffID:      staffID,
		PublisherAccountID: publisherID,
		Vertical:           input.Body.Vertical,
		Name:               input.Body.Name,
		Slug:               input.Body.Slug,
		ShortDescription:   input.Body.ShortDescription,
		LongDescription:    input.Body.LongDescription,
		Tags:               input.Body.Tags,
		BundleType:         input.Body.BundleType,
		PricingType:        input.Body.PricingType,
		PriceCents:         input.Body.PriceCents,
		Currency:           input.Body.Currency,
		PreviewFieldCount:  input.Body.PreviewFieldCount,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &listingHTTPResponse{Body: resp}, nil
}

// ── Admin: publish version from tenant form ───────────────────────────────────

type publishVersionInput struct {
	ListingID string `path:"listing_id" doc:"The listing to publish a new version for."`
	Body      struct {
		SourceFormID  string  `json:"source_form_id"   doc:"The tenant form (owned by the caller's clinic) whose latest published version will be snapshotted."`
		ChangeType    string  `json:"change_type"      enum:"major,minor"`
		ChangeSummary *string `json:"change_summary,omitempty"`
	}
}

func (h *Handler) publishVersion(ctx context.Context, input *publishVersionInput) (*versionHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	formID, err := uuid.Parse(input.Body.SourceFormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid source_form_id")
	}

	resp, err := h.svc.PublishVersion(ctx, PublishVersionInput{
		ListingID:     listingID,
		ClinicID:      clinicID, // caller's clinic = source clinic
		SourceFormID:  formID,
		StaffID:       staffID,
		ChangeType:    input.Body.ChangeType,
		ChangeSummary: input.Body.ChangeSummary,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &versionHTTPResponse{Body: resp}, nil
}

// ── Admin: publish listing (draft → published) ────────────────────────────────

type publishListingInput struct {
	ListingID string `path:"listing_id" doc:"The listing to publish."`
}

func (h *Handler) publishListing(ctx context.Context, input *publishListingInput) (*listingHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	resp, err := h.svc.PublishListingByOwner(ctx, clinicID, listingID)
	if err != nil {
		return nil, mapError(err)
	}
	return &listingHTTPResponse{Body: resp}, nil
}

// ── Admin: ensure publisher ───────────────────────────────────────────────────

type publisherHTTPResponse struct {
	Body *PublisherResponse
}

func (h *Handler) registerPublisher(ctx context.Context, input *registerPublisherBodyInput) (*publisherHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.RegisterPublisher(ctx, RegisterPublisherInput{
		ClinicID:    clinicID,
		DisplayName: input.Body.DisplayName,
		Bio:         input.Body.Bio,
		WebsiteURL:  input.Body.WebsiteURL,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &publisherHTTPResponse{Body: resp}, nil
}

type registerPublisherBodyInput struct {
	Body struct {
		DisplayName string  `json:"display_name" minLength:"1"`
		Bio         *string `json:"bio,omitempty"`
		WebsiteURL  *string `json:"website_url,omitempty"`
	}
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	case errors.Is(err, domain.ErrValidation):
		return huma.Error400BadRequest(err.Error())
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
