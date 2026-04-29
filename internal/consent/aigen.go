package consent

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/aigen"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// AIGenClinicLookup resolves the (vertical, country, tier) the aigen
// prompt builder needs. Same shape as forms.AIGenClinicLookup so the
// existing app.go adapter satisfies both.
type AIGenClinicLookup interface {
	GetForAIGen(ctx context.Context, clinicID uuid.UUID) (vertical, country, tier string, err error)
}

// AIGenHandler exposes the consent-draft endpoint. Returns the AI's draft
// directly to the caller — Salvia NEVER auto-saves consent text. The
// clinician reviews and edits before submitting via the regular capture
// endpoint, which provides the real audit-defensible trail.
type AIGenHandler struct {
	gen       *aigen.ConsentDraftService
	clinics   AIGenClinicLookup
	rateLimit *mw.RateLimiterStore
}

func NewAIGenHandler(gen *aigen.ConsentDraftService, clinics AIGenClinicLookup, rateLimit *mw.RateLimiterStore) *AIGenHandler {
	return &AIGenHandler{gen: gen, clinics: clinics, rateLimit: rateLimit}
}

type aiDraftConsentBody struct {
	Body struct {
		Procedure   string `json:"procedure"   minLength:"5" maxLength:"500" doc:"Free-text description of the procedure or scope being consented to (e.g. 'general anaesthesia for routine dental cleaning')."`
		ConsentType string `json:"consent_type" enum:"audio_recording,ai_processing,telemedicine,sedation,euthanasia,invasive_procedure,mhr_write,photography,data_sharing,controlled_drug_administration,treatment_plan,other"`
		Audience    string `json:"audience,omitempty"    enum:"self,owner,guardian,epoa,nok,authorised_representative,other" doc:"Who is consenting? Shapes the second-person tone of the draft text."`
	}
}

type aiDraftConsentResponse struct {
	Body struct {
		RisksDiscussed        string `json:"risks_discussed"`
		AlternativesDiscussed string `json:"alternatives_discussed"`
		Provider              string `json:"provider"`
		Model                 string `json:"model"`
		PromptHash            string `json:"prompt_hash"`
	}
}

// Mount registers the consent-draft route. If the AI provider isn't
// configured the route is omitted entirely so the OpenAPI surface only
// advertises what's callable.
func (h *AIGenHandler) Mount(_ any, api huma.API, jwtSecret []byte) {
	if h == nil || h.gen == nil {
		return
	}
	auth := mw.AuthenticateHuma(api, jwtSecret)
	manage := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManagePatients
	})
	security := []map[string][]string{{"bearerAuth": {}}}

	mws := huma.Middlewares{auth, manage}
	if h.rateLimit != nil {
		mws = append(huma.Middlewares{mw.RateLimitHuma(api, h.rateLimit)}, mws...)
	}

	huma.Register(api, huma.Operation{
		OperationID:   "generate-consent-draft",
		Method:        http.MethodPost,
		Path:          "/api/v1/consent/ai-draft",
		Summary:       "Draft consent risks + alternatives text",
		Description:   "Returns AI-drafted text for the risks_discussed and alternatives_discussed fields of a consent record. The clinician reviews and edits before submitting via /api/v1/consent. Salvia never auto-saves consent text — this endpoint is a drafting aid only.",
		Tags:          []string{"Consent"},
		Security:      security,
		Middlewares:   mws,
		DefaultStatus: http.StatusOK,
	}, h.draftConsent)
}

func (h *AIGenHandler) draftConsent(ctx context.Context, input *aiDraftConsentBody) (*aiDraftConsentResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	vertical, country, tier, err := h.clinics.GetForAIGen(ctx, clinicID)
	if err != nil {
		return nil, huma.Error500InternalServerError(fmt.Sprintf("clinic lookup: %v", err))
	}

	res, err := h.gen.Generate(ctx, aigen.ConsentDraftRequest{
		Clinic: aigen.ClinicContext{
			ClinicID: clinicID.String(),
			Vertical: vertical,
			Country:  country,
			Tier:     tier,
		},
		StaffID:     staffID.String(),
		Procedure:   input.Body.Procedure,
		ConsentType: input.Body.ConsentType,
		Audience:    input.Body.Audience,
	})
	if err != nil {
		return nil, mapAIDraftError(err)
	}

	resp := &aiDraftConsentResponse{}
	resp.Body.RisksDiscussed = res.Draft.RisksDiscussed
	resp.Body.AlternativesDiscussed = res.Draft.AlternativesDiscussed
	resp.Body.Provider = res.Metadata.Provider
	resp.Body.Model = res.Metadata.Model
	resp.Body.PromptHash = res.Metadata.PromptHash
	return resp, nil
}

func mapAIDraftError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return huma.NewError(http.StatusRequestTimeout, "request cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return huma.NewError(http.StatusGatewayTimeout, "AI provider timeout")
	default:
		return huma.Error502BadGateway(fmt.Sprintf("AI provider error: %v", err))
	}
}
