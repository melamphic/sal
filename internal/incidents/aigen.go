package incidents

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
// prompt builder needs. Same shape as forms.AIGenClinicLookup.
type AIGenClinicLookup interface {
	GetForAIGen(ctx context.Context, clinicID uuid.UUID) (vertical, country, tier string, err error)
}

// AIGenHandler exposes the incident-draft endpoint. Returns the AI's draft
// directly to the caller; the SIRS / CQC classifier still runs server-side
// on the final values when the clinician submits via the regular create
// endpoint, so AI never bypasses the regulator-decision logic.
type AIGenHandler struct {
	gen       *aigen.IncidentDraftService
	clinics   AIGenClinicLookup
	rateLimit *mw.RateLimiterStore
}

func NewAIGenHandler(gen *aigen.IncidentDraftService, clinics AIGenClinicLookup, rateLimit *mw.RateLimiterStore) *AIGenHandler {
	return &AIGenHandler{gen: gen, clinics: clinics, rateLimit: rateLimit}
}

type aiDraftIncidentBody struct {
	Body struct {
		Account string `json:"account" minLength:"20" maxLength:"4000" doc:"Free-text or audio-transcribed account of the incident. The AI extracts typed fields; the clinician reviews and edits before submitting."`
	}
}

type aiDraftIncidentResponse struct {
	Body struct {
		IncidentType     string `json:"incident_type"`
		Severity         string `json:"severity"`
		BriefDescription string `json:"brief_description"`
		ImmediateActions string `json:"immediate_actions,omitempty"`
		WitnessesText    string `json:"witnesses_text,omitempty"`
		SubjectOutcome   string `json:"subject_outcome,omitempty"`
		Location         string `json:"location,omitempty"`
		Provider         string `json:"provider"`
		Model            string `json:"model"`
		PromptHash       string `json:"prompt_hash"`
	}
}

// Mount registers the incident-draft route. Omitted when no AI provider
// is configured.
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
		OperationID:   "generate-incident-draft",
		Method:        http.MethodPost,
		Path:          "/api/v1/incidents/ai-draft",
		Summary:       "Extract structured fields from an incident account",
		Description:   "Returns AI-drafted typed fields (type, severity, description, actions, witnesses, outcome, location) extracted from a free-text or transcribed account. The clinician reviews and edits before submitting via /api/v1/incidents — SIRS / CQC classification runs server-side on the final committed values, so AI suggestions never bypass the regulator-decision logic.",
		Tags:          []string{"Incidents"},
		Security:      security,
		Middlewares:   mws,
		DefaultStatus: http.StatusOK,
	}, h.draftIncident)
}

func (h *AIGenHandler) draftIncident(ctx context.Context, input *aiDraftIncidentBody) (*aiDraftIncidentResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	vertical, country, tier, err := h.clinics.GetForAIGen(ctx, clinicID)
	if err != nil {
		return nil, huma.Error500InternalServerError(fmt.Sprintf("clinic lookup: %v", err))
	}

	res, err := h.gen.Generate(ctx, aigen.IncidentDraftRequest{
		Clinic: aigen.ClinicContext{
			ClinicID: clinicID.String(),
			Vertical: vertical,
			Country:  country,
			Tier:     tier,
		},
		StaffID: staffID.String(),
		Account: input.Body.Account,
	})
	if err != nil {
		return nil, mapAIDraftError(err)
	}

	resp := &aiDraftIncidentResponse{}
	resp.Body.IncidentType = res.Draft.IncidentType
	resp.Body.Severity = res.Draft.Severity
	resp.Body.BriefDescription = res.Draft.BriefDescription
	resp.Body.ImmediateActions = res.Draft.ImmediateActions
	resp.Body.WitnessesText = res.Draft.WitnessesText
	resp.Body.SubjectOutcome = res.Draft.SubjectOutcome
	resp.Body.Location = res.Draft.Location
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
