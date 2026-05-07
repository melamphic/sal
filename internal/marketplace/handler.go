package marketplace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

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
		BundleType         string   `json:"bundle_type,omitempty" enum:"bundled,form_only,pack,policy_only" default:"bundled" doc:"'bundled' = form + linked policies; 'form_only' = form, no policies; 'pack' = N forms; 'policy_only' = a tenant policy."`
		PricingType        string   `json:"pricing_type"         enum:"free,paid"`
		PriceCents         *int     `json:"price_cents,omitempty"`
		Currency           string   `json:"currency,omitempty"   default:"NZD"`
		PreviewFieldCount  int      `json:"preview_field_count,omitempty" minimum:"0" default:"3"`
		// Pack: required when bundle_type='pack'. At least 2 IDs.
		SourceFormIDs []string `json:"source_form_ids,omitempty"`
		// Policy-only: required when bundle_type='policy_only'.
		SourcePolicyID *string `json:"source_policy_id,omitempty"`
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

	var sourceFormIDs []uuid.UUID
	if len(input.Body.SourceFormIDs) > 0 {
		sourceFormIDs = make([]uuid.UUID, len(input.Body.SourceFormIDs))
		for i, s := range input.Body.SourceFormIDs {
			parsed, err := uuid.Parse(s)
			if err != nil {
				return nil, huma.Error400BadRequest("invalid source_form_ids[" + fmt.Sprint(i) + "]")
			}
			sourceFormIDs[i] = parsed
		}
	}
	var sourcePolicyID *uuid.UUID
	if input.Body.SourcePolicyID != nil && *input.Body.SourcePolicyID != "" {
		parsed, err := uuid.Parse(*input.Body.SourcePolicyID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid source_policy_id")
		}
		sourcePolicyID = &parsed
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
		SourceFormIDs:      sourceFormIDs,
		SourcePolicyID:     sourcePolicyID,
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

// ── My publisher listings ────────────────────────────────────────────────────

type listMyPublisherListingsInput struct {
	paginationInput
}

func (h *Handler) listMyPublisherListings(ctx context.Context, input *listMyPublisherListingsInput) (*listingListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.ListMyPublisherListings(ctx, clinicID, input.Limit, input.Offset)
	if err != nil {
		return nil, mapError(err)
	}
	return &listingListHTTPResponse{Body: resp}, nil
}

// ── Reviews ──────────────────────────────────────────────────────────────────

type createReviewInput struct {
	AcquisitionID string `path:"acquisition_id" doc:"Acquisition id — establishes verified purchase."`
	Body          struct {
		Rating int     `json:"rating" minimum:"1" maximum:"5"`
		Body   *string `json:"body,omitempty"`
	}
}

type reviewHTTPResponse struct {
	Body *ReviewResponse
}

func (h *Handler) createReview(ctx context.Context, input *createReviewInput) (*reviewHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	acqID, err := uuid.Parse(input.AcquisitionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid acquisition_id")
	}
	resp, err := h.svc.CreateReview(ctx, CreateReviewInput{
		AcquisitionID: acqID,
		ClinicID:      clinicID,
		StaffID:       staffID,
		Rating:        input.Body.Rating,
		Body:          input.Body.Body,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &reviewHTTPResponse{Body: resp}, nil
}

type listReviewsInput struct {
	paginationInput
	ListingID string `path:"listing_id"`
}

type reviewListHTTPResponse struct {
	Body *ReviewListResponse
}

func (h *Handler) listReviews(ctx context.Context, input *listReviewsInput) (*reviewListHTTPResponse, error) {
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	resp, err := h.svc.ListReviews(ctx, listingID, input.Limit, input.Offset)
	if err != nil {
		return nil, mapError(err)
	}
	return &reviewListHTTPResponse{Body: resp}, nil
}

// ── Upgrade notifications ────────────────────────────────────────────────────

type listMyNotificationsInput struct {
	Limit int `query:"limit" minimum:"1" maximum:"100" default:"20"`
}

type notificationsHTTPResponse struct {
	Body struct {
		Items []*UpgradeNotificationResponse `json:"items"`
	}
}

func (h *Handler) listMyNotifications(ctx context.Context, input *listMyNotificationsInput) (*notificationsHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	items, err := h.svc.ListMyUpgradeNotifications(ctx, clinicID, input.Limit)
	if err != nil {
		return nil, mapError(err)
	}
	resp := &notificationsHTTPResponse{}
	resp.Body.Items = items
	return resp, nil
}

type markNotificationSeenInput struct {
	NotificationID string `path:"notification_id"`
}

type emptyHTTPResponse struct{}

func (h *Handler) markNotificationSeen(ctx context.Context, input *markNotificationSeenInput) (*emptyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	notID, err := uuid.Parse(input.NotificationID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid notification_id")
	}
	if err := h.svc.MarkNotificationSeen(ctx, notID, clinicID); err != nil {
		return nil, mapError(err)
	}
	return &emptyHTTPResponse{}, nil
}

// ── Badges ───────────────────────────────────────────────────────────────────

type grantBadgeInput struct {
	TargetPublisherID string `path:"publisher_id"`
	Body              struct {
		VerifiedBadge bool    `json:"verified_badge"`
		AuthorityType *string `json:"authority_type,omitempty" enum:"salvia,authority" doc:"Optional — requires Salvia grantor. Pass null to keep existing."`
	}
}

func (h *Handler) grantBadge(ctx context.Context, input *grantBadgeInput) (*publisherHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	targetID, err := uuid.Parse(input.TargetPublisherID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid publisher_id")
	}
	resp, err := h.svc.GrantBadge(ctx, GrantBadgeInput{
		GranterClinicID:   clinicID,
		TargetPublisherID: targetID,
		VerifiedBadge:     input.Body.VerifiedBadge,
		AuthorityType:     input.Body.AuthorityType,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &publisherHTTPResponse{Body: resp}, nil
}

type revokeBadgeInput struct {
	TargetPublisherID string `path:"publisher_id"`
}

func (h *Handler) revokeBadge(ctx context.Context, input *revokeBadgeInput) (*publisherHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	targetID, err := uuid.Parse(input.TargetPublisherID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid publisher_id")
	}
	resp, err := h.svc.RevokeBadge(ctx, clinicID, targetID)
	if err != nil {
		return nil, mapError(err)
	}
	return &publisherHTTPResponse{Body: resp}, nil
}

// ── Suspend listing ──────────────────────────────────────────────────────────

type suspendListingInput struct {
	ListingID string `path:"listing_id"`
}

func (h *Handler) suspendListing(ctx context.Context, input *suspendListingInput) (*listingHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	resp, err := h.svc.SuspendListing(ctx, clinicID, listingID)
	if err != nil {
		return nil, mapError(err)
	}
	return &listingHTTPResponse{Body: resp}, nil
}

// ── Update listing (PATCH) ───────────────────────────────────────────────────

// updateListingInput mirrors the partial-update contract: every field is
// optional. The server applies the per-status policy and rejects edits to
// identity / pricing fields once the listing is published.
type updateListingInput struct {
	ListingID string `path:"listing_id"`
	Body      struct {
		Name              *string   `json:"name,omitempty"             minLength:"1"`
		ShortDescription  *string   `json:"short_description,omitempty" minLength:"1"`
		LongDescription   *string   `json:"long_description,omitempty"`
		Tags              *[]string `json:"tags,omitempty"`
		BundleType        *string   `json:"bundle_type,omitempty"      enum:"bundled,form_only"`
		PricingType       *string   `json:"pricing_type,omitempty"     enum:"free,paid"`
		PriceCents        *int      `json:"price_cents,omitempty"`
		Currency          *string   `json:"currency,omitempty"`
		PreviewFieldCount *int      `json:"preview_field_count,omitempty" minimum:"0"`
	}
}

func (h *Handler) updateListing(ctx context.Context, input *updateListingInput) (*listingHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	resp, err := h.svc.UpdateListingByOwner(ctx, UpdateListingInput{
		CallerClinicID:    clinicID,
		ListingID:         listingID,
		Name:              input.Body.Name,
		ShortDescription:  input.Body.ShortDescription,
		LongDescription:   input.Body.LongDescription,
		Tags:              input.Body.Tags,
		BundleType:        input.Body.BundleType,
		PricingType:       input.Body.PricingType,
		PriceCents:        input.Body.PriceCents,
		Currency:          input.Body.Currency,
		PreviewFieldCount: input.Body.PreviewFieldCount,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &listingHTTPResponse{Body: resp}, nil
}

// ── Archive listing (publisher self-serve) ───────────────────────────────────

type archiveListingInput struct {
	ListingID string `path:"listing_id"`
}

func (h *Handler) archiveListing(ctx context.Context, input *archiveListingInput) (*listingHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	resp, err := h.svc.ArchiveListingByOwner(ctx, clinicID, listingID)
	if err != nil {
		return nil, mapError(err)
	}
	return &listingHTTPResponse{Body: resp}, nil
}

// ── Delete listing (drafts only) ─────────────────────────────────────────────

type deleteListingInput struct {
	ListingID string `path:"listing_id"`
}

type deleteListingHTTPResponse struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

func (h *Handler) deleteListing(ctx context.Context, input *deleteListingInput) (*deleteListingHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	if err := h.svc.DeleteListingDraftByOwner(ctx, clinicID, listingID); err != nil {
		return nil, mapError(err)
	}
	resp := &deleteListingHTTPResponse{}
	resp.Body.Deleted = true
	return resp, nil
}

// ── Pack listing source-form composition ─────────────────────────────────────

type listPackFormsInput struct {
	ListingID string `path:"listing_id"`
}

type packFormsHTTPResponse struct {
	Body struct {
		Items []packFormItem `json:"items"`
	}
}

type packFormItem struct {
	Position     int    `json:"position"`
	SourceFormID string `json:"source_form_id"`
}

func (h *Handler) listPackForms(ctx context.Context, input *listPackFormsInput) (*packFormsHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	rows, err := h.svc.ListPackFormsByOwner(ctx, clinicID, listingID)
	if err != nil {
		return nil, mapError(err)
	}
	resp := &packFormsHTTPResponse{}
	resp.Body.Items = make([]packFormItem, len(rows))
	for i, r := range rows {
		resp.Body.Items[i] = packFormItem{
			Position:     r.Position,
			SourceFormID: r.SourceFormID.String(),
		}
	}
	return resp, nil
}

type setPackFormsInput struct {
	ListingID string `path:"listing_id"`
	Body      struct {
		SourceFormIDs []string `json:"source_form_ids" minItems:"2" doc:"Ordered list of tenant form UUIDs (>=2). Replaces the entire pack composition."`
	}
}

func (h *Handler) setPackForms(ctx context.Context, input *setPackFormsInput) (*emptyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	formIDs := make([]uuid.UUID, len(input.Body.SourceFormIDs))
	for i, s := range input.Body.SourceFormIDs {
		parsed, err := uuid.Parse(s)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid source_form_ids[" + fmt.Sprint(i) + "]")
		}
		formIDs[i] = parsed
	}
	if err := h.svc.SetPackFormsByOwner(ctx, clinicID, listingID, formIDs); err != nil {
		return nil, mapError(err)
	}
	return &emptyHTTPResponse{}, nil
}

// ── Stripe Connect onboarding ────────────────────────────────────────────────

type startOnboardingInput struct {
	PublisherID string `path:"publisher_id"`
	Body        struct {
		Email      string `json:"email" format:"email"`
		Country    string `json:"country" minLength:"2" maxLength:"2" doc:"ISO-3166-1 alpha-2 country code."`
		RefreshURL string `json:"refresh_url" format:"uri"`
		ReturnURL  string `json:"return_url" format:"uri"`
	}
}

type onboardingHTTPResponse struct {
	Body struct {
		OnboardingURL string `json:"onboarding_url"`
	}
}

func (h *Handler) startOnboarding(ctx context.Context, input *startOnboardingInput) (*onboardingHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	pubID, err := uuid.Parse(input.PublisherID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid publisher_id")
	}
	url, err := h.svc.StartPublisherOnboarding(ctx, StripeConnectOnboardingInput{
		PublisherID: pubID,
		ClinicID:    clinicID,
		Email:       input.Body.Email,
		Country:     input.Body.Country,
		RefreshURL:  input.Body.RefreshURL,
		ReturnURL:   input.Body.ReturnURL,
	})
	if err != nil {
		return nil, mapError(err)
	}
	resp := &onboardingHTTPResponse{}
	resp.Body.OnboardingURL = url
	return resp, nil
}

// ── Purchase ─────────────────────────────────────────────────────────────────

type purchaseInput struct {
	ListingID string `path:"listing_id"`
}

type purchaseHTTPResponse struct {
	Body *PurchaseResponse
}

func (h *Handler) purchaseListing(ctx context.Context, input *purchaseInput) (*purchaseHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	listingID, err := uuid.Parse(input.ListingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid listing_id")
	}
	resp, err := h.svc.Purchase(ctx, PurchaseInput{
		ListingID: listingID,
		ClinicID:  clinicID,
		StaffID:   staffID,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &purchaseHTTPResponse{Body: resp}, nil
}

// ── Stripe webhook ───────────────────────────────────────────────────────────
//
// The webhook is mounted as a raw Chi handler (not Huma) because we need the
// raw request body for signature verification — Huma consumes and re-serialises
// which would break Stripe-Signature.

// StripeWebhookHandler returns an http.HandlerFunc that verifies and dispatches
// Stripe webhook events via the service.
func (h *Handler) StripeWebhookHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		signature := r.Header.Get("Stripe-Signature")
		if err := h.svc.HandleStripeWebhook(r.Context(), payload, signature); err != nil {
			// Log and 400 — Stripe will retry on non-2xx.
			http.Error(w, "webhook error", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
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
