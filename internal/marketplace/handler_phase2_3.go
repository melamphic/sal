package marketplace

import (
	"context"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

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
