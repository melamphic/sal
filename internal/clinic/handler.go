package clinic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"

	"github.com/danielgtaylor/huma/v2"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires clinic HTTP endpoints to the clinic Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new clinic Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Request / Response types ──────────────────────────────────────────────────

type registerInput struct {
	Body struct {
		Name       string          `json:"name" minLength:"2" maxLength:"200" doc:"The clinic's display name."`
		Email      string          `json:"email" format:"email" doc:"The clinic's primary contact email. Used for billing and admin notifications."`
		Phone      *string         `json:"phone,omitempty" doc:"Clinic phone number."`
		Address    *string         `json:"address,omitempty" doc:"Clinic physical address."`
		Vertical   domain.Vertical `json:"vertical" enum:"veterinary,dental,aged_care" doc:"The clinical domain this clinic operates in."`
		DataRegion string          `json:"data_region" doc:"Where clinic data is stored (e.g. ap-southeast-2, eu-west-2)." default:"ap-southeast-2"`
		AdminEmail string          `json:"admin_email" format:"email" doc:"Email of the first super admin. A magic link is sent here after registration."`
		AdminName  string          `json:"admin_name" minLength:"1" maxLength:"200" doc:"Full name of the first super admin."`
	}
}

type updateInput struct {
	Body struct {
		Name               *string `json:"name,omitempty"                minLength:"2" maxLength:"200" doc:"Updated clinic name."`
		Phone              *string `json:"phone,omitempty"               doc:"Updated phone number."`
		Address            *string `json:"address,omitempty"             doc:"Updated physical address."`
		AccentColor        *string `json:"accent_color,omitempty"        doc:"Brand accent colour, hex (e.g. #4F8A4D)."`
		PDFHeaderText      *string `json:"pdf_header_text,omitempty"     doc:"Text rendered in the header of generated PDFs."`
		PDFFooterText      *string `json:"pdf_footer_text,omitempty"     doc:"Text rendered in the footer of generated PDFs."`
		PDFPrimaryColor    *string `json:"pdf_primary_color,omitempty"   doc:"Primary colour used in generated PDFs, hex."`
		PDFFont            *string `json:"pdf_font,omitempty"            enum:"inter,plus_jakarta_sans,lora,jetbrains_mono" doc:"Font family used in generated PDFs."`
		OnboardingStep     *int16  `json:"onboarding_step,omitempty"     minimum:"0" maximum:"4" doc:"Current onboarding step (0..3). Stops being meaningful once onboarding_complete is true."`
		OnboardingComplete *bool   `json:"onboarding_complete,omitempty" doc:"Set to true to mark first-run setup finished."`
	}
}

type clinicResponse struct {
	Body *ClinicResponse
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// register handles POST /api/v1/clinic/register.
// This is a public endpoint — it creates the clinic and the first super admin.
func (h *Handler) register(ctx context.Context, input *registerInput) (*clinicResponse, error) {
	dto, err := h.svc.Register(ctx, RegisterInput{
		Name:       input.Body.Name,
		Email:      input.Body.Email,
		Phone:      input.Body.Phone,
		Address:    input.Body.Address,
		Vertical:   input.Body.Vertical,
		DataRegion: input.Body.DataRegion,
		AdminEmail: input.Body.AdminEmail,
		AdminName:  input.Body.AdminName,
	})
	if err != nil {
		return nil, mapClinicError(err)
	}
	return &clinicResponse{Body: dto}, nil
}

// get handles GET /api/v1/clinic.
// Returns the authenticated clinic's details.
func (h *Handler) get(ctx context.Context, _ *struct{}) (*clinicResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	dto, err := h.svc.GetByID(ctx, clinicID)
	if err != nil {
		return nil, mapClinicError(err)
	}
	return &clinicResponse{Body: dto}, nil
}

// update handles PATCH /api/v1/clinic.
// Updates mutable clinic settings.
func (h *Handler) update(ctx context.Context, input *updateInput) (*clinicResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	dto, err := h.svc.Update(ctx, clinicID, UpdateInput{
		Name:               input.Body.Name,
		Phone:              input.Body.Phone,
		Address:            input.Body.Address,
		AccentColor:        input.Body.AccentColor,
		PDFHeaderText:      input.Body.PDFHeaderText,
		PDFFooterText:      input.Body.PDFFooterText,
		PDFPrimaryColor:    input.Body.PDFPrimaryColor,
		PDFFont:            input.Body.PDFFont,
		OnboardingStep:     input.Body.OnboardingStep,
		OnboardingComplete: input.Body.OnboardingComplete,
	})
	if err != nil {
		return nil, mapClinicError(err)
	}
	return &clinicResponse{Body: dto}, nil
}

// uploadLogoInput is multipart form input for the logo upload endpoint.
type uploadLogoInput struct {
	RawBody multipart.Form
}

// maxLogoBytes caps logo uploads at 4 MiB. Real-world clinic logos are small.
const maxLogoBytes int64 = 4 << 20

// uploadLogo handles POST /api/v1/clinic/logo (multipart/form-data, field "file").
func (h *Handler) uploadLogo(ctx context.Context, input *uploadLogoInput) (*clinicResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	files := input.RawBody.File["file"]
	if len(files) == 0 {
		return nil, huma.Error400BadRequest("missing form field \"file\"")
	}
	hdr := files[0]
	if hdr.Size > maxLogoBytes {
		return nil, huma.Error400BadRequest(fmt.Sprintf("logo too large (max %d bytes)", maxLogoBytes))
	}

	contentType := hdr.Header.Get("Content-Type")
	if !isAllowedLogoType(contentType) {
		return nil, huma.Error415UnsupportedMediaType("logo must be png, jpeg, svg or webp")
	}

	f, err := hdr.Open()
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read uploaded file")
	}
	defer func() { _ = f.Close() }()

	// multipart.File is a ReadSeeker, which the AWS SDK requires for signed
	// uploads over plain HTTP (MinIO). Don't wrap in io.LimitReader — that
	// hides Seek. Size was already validated above.
	dto, err := h.svc.UploadLogo(ctx, clinicID, contentType, f, hdr.Size)
	if err != nil {
		return nil, mapClinicError(err)
	}
	return &clinicResponse{Body: dto}, nil
}

func isAllowedLogoType(ct string) bool {
	switch ct {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/svg+xml":
		return true
	}
	return false
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapClinicError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("clinic not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("a clinic with this email already exists")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		// Unhandled — surface in server logs so dev/ops can diagnose.
		slog.Error("clinic handler: unmapped error", slog.String("error", err.Error()))
		return huma.Error500InternalServerError("internal server error")
	}
}
