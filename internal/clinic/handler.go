package clinic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"strings"

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
		Vertical   domain.Vertical `json:"vertical" enum:"veterinary,dental,general_clinic,aged_care" doc:"The clinical domain this clinic operates in."`
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
		OnboardingStep     *int16  `json:"onboarding_step,omitempty"     minimum:"0" maximum:"5" doc:"Current onboarding step (0=clinic_profile, 1=compliance, 2=invite_team, 3=pdf_brand, 4=tour, 5=done). Stops being meaningful once onboarding_complete is true."`
		OnboardingComplete *bool   `json:"onboarding_complete,omitempty" doc:"Set to true to mark first-run setup finished."`
		LegalName          *string `json:"legal_name,omitempty"          maxLength:"200" doc:"Registered legal / trading name (e.g. 'Greenwood Veterinary Ltd'). Appears on invoices."`
		Country            *string `json:"country,omitempty"             enum:"NZ,AU,GB,IN" doc:"ISO 3166-1 alpha-2 country code. Drives the business-registration label (NZBN / ABN / CRN / GSTIN)."`
		Timezone           *string `json:"timezone,omitempty"            doc:"IANA timezone (e.g. 'Pacific/Auckland')."`
		BusinessRegNo      *string `json:"business_reg_no,omitempty"     maxLength:"64" doc:"Business registration identifier — NZBN, ABN, CRN, GSTIN, etc., depending on country."`
		AcceptTerms        *bool   `json:"accept_terms,omitempty"        doc:"Set to true to accept the Salvia terms of service. Stamps terms_accepted_at. Cannot be unset."`
	}
}

type clinicResponse struct {
	Body *ClinicResponse
}

// complianceInput is the body submitted from the onboarding wizard's
// compliance step. The `header:` fields capture the client IP from the
// reverse proxy for audit — both X-Forwarded-For and X-Real-Ip are read
// because deployments vary (Cloudflare → X-Forwarded-For; Caddy/nginx
// behind Cloudflare → X-Real-Ip already normalised).
type complianceInput struct {
	XForwardedFor string `header:"X-Forwarded-For"`
	XRealIP       string `header:"X-Real-Ip"`
	Body          struct {
		PrivacyOfficerName         string `json:"privacy_officer_name"          minLength:"2" maxLength:"200" doc:"Designated Privacy Officer's full name. NZ Privacy Act 2020 s 201 mandates a Privacy Officer for every agency handling personal information; AU treats it as best practice (APP 1)."`
		PrivacyOfficerEmail        string `json:"privacy_officer_email"         format:"email" doc:"Contact email for the Privacy Officer. Published on the clinic's privacy notice; treated as public-facing contact info."`
		PrivacyOfficerPhone        string `json:"privacy_officer_phone,omitempty" maxLength:"50" doc:"Optional Privacy Officer phone number."`
		POTrainingAttested         bool   `json:"po_training_attested"            doc:"Attestation that the Privacy Officer has completed privacy-program training."`
		CrossBorderAcknowledged    bool   `json:"cross_border_acknowledged"       doc:"Acknowledgement that audio + transcripts may be processed by Deepgram (US) and Google Vertex AI (configurable region). Required by AU APP 8 and NZ HIPC Rule 12."`
		MHRRegistered              *bool  `json:"mhr_registered,omitempty"        doc:"AU only: whether the clinic is registered to write to My Health Record. Null outside AU."`
		AIOversightAcknowledged    bool   `json:"ai_oversight_acknowledged"       doc:"Clinician acknowledges AI is decision-support; every output is reviewed by a human before being signed. AU Voluntary AI Safety Standard 2024 + OAIC AI guidance."`
		PatientConsentAcknowledged bool   `json:"patient_consent_acknowledged"    doc:"Clinic confirms responsibility for obtaining patient consent for audio capture and AI processing prior to recording."`
		DPAAccepted                bool   `json:"dpa_accepted"                    doc:"Acceptance of Salvia's Data Processing Agreement (versioned)."`
	}
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
		LegalName:          input.Body.LegalName,
		Country:            input.Body.Country,
		Timezone:           input.Body.Timezone,
		BusinessRegNo:      input.Body.BusinessRegNo,
		AcceptTerms:        input.Body.AcceptTerms,
	})
	if err != nil {
		return nil, mapClinicError(err)
	}
	return &clinicResponse{Body: dto}, nil
}

// submitCompliance handles POST /api/v1/clinic/compliance.
// Stamps server-side timestamps on every attestation, captures the staff
// id + IP for audit, and advances the onboarding cursor past the
// compliance step. Re-submission updates the existing record (idempotent).
func (h *Handler) submitCompliance(ctx context.Context, input *complianceInput) (*clinicResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	ip := input.XRealIP
	if ip == "" {
		ip = firstForwardedIP(input.XForwardedFor)
	}

	dto, err := h.svc.SubmitCompliance(ctx, clinicID, SubmitComplianceInput{
		PrivacyOfficerName:         input.Body.PrivacyOfficerName,
		PrivacyOfficerEmail:        input.Body.PrivacyOfficerEmail,
		PrivacyOfficerPhone:        input.Body.PrivacyOfficerPhone,
		POTrainingAttested:         input.Body.POTrainingAttested,
		CrossBorderAcknowledged:    input.Body.CrossBorderAcknowledged,
		MHRRegistered:              input.Body.MHRRegistered,
		AIOversightAcknowledged:    input.Body.AIOversightAcknowledged,
		PatientConsentAcknowledged: input.Body.PatientConsentAcknowledged,
		DPAAccepted:                input.Body.DPAAccepted,
		IP:                         ip,
		StaffID:                    staffID,
	})
	if err != nil {
		return nil, mapClinicError(err)
	}
	return &clinicResponse{Body: dto}, nil
}

// firstForwardedIP returns the left-most address from a comma-separated
// X-Forwarded-For header (the original client). Empty string when the
// header is absent.
func firstForwardedIP(xff string) string {
	if xff == "" {
		return ""
	}
	if i := strings.IndexByte(xff, ','); i > 0 {
		return strings.TrimSpace(xff[:i])
	}
	return strings.TrimSpace(xff)
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
