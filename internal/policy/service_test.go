package policy

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// fakeTemplateOverlay is a hand-rolled fake for TemplateOverlaySource. The
// policy service's repository is a concrete struct (no interface) so the
// overlay path is exercised via a focused unit test on the helper itself
// instead of a full end-to-end ListClauses test, which would require a
// running database.
type fakeTemplateOverlay struct {
	byID map[string][]TemplateClause
}

func (f *fakeTemplateOverlay) ClausesForTemplate(_ context.Context, templateID string, _ uuid.UUID) ([]TemplateClause, bool) {
	v, ok := f.byID[templateID]
	return v, ok
}

func TestService_OverlayTemplateClauses_FillsEmptyDefaultRow(t *testing.T) {
	tID := "salvia.consent_to_treatment"
	tState := "default"
	pol := &PolicyRecord{
		ID:                  uuid.New(),
		ClinicID:            uuid.New(),
		SalviaTemplateID:    &tID,
		SalviaTemplateState: &tState,
	}
	resp := &PolicyClauseListResponse{Items: []*PolicyClauseResponse{}}
	svc := &Service{
		templates: &fakeTemplateOverlay{byID: map[string][]TemplateClause{
			tID: {
				{ID: "scope", Title: "Scope", Body: "This consent covers …"},
				{ID: "withdrawal", Title: "Withdrawal", Body: "Patients may withdraw …"},
			},
		}},
	}

	svc.overlayTemplateClauses(context.Background(), pol, resp)

	if got := len(resp.Items); got != 2 {
		t.Fatalf("overlay clause count: got %d, want 2", got)
	}
	if resp.Items[0].Title != "Scope" {
		t.Errorf("clause[0] title: %q", resp.Items[0].Title)
	}
	if resp.Items[0].BlockID != "scope" {
		t.Errorf("clause[0] block_id: %q", resp.Items[0].BlockID)
	}
	if resp.Items[0].Parity == "" {
		t.Error("clause parity should default to non-empty so the FE pill renders")
	}
}

func TestService_OverlayTemplateClauses_SkipsForkedRow(t *testing.T) {
	tID := "salvia.consent_to_treatment"
	tForked := "forked"
	pol := &PolicyRecord{
		ID:                  uuid.New(),
		ClinicID:            uuid.New(),
		SalviaTemplateID:    &tID,
		SalviaTemplateState: &tForked,
	}
	resp := &PolicyClauseListResponse{Items: []*PolicyClauseResponse{}}
	svc := &Service{
		templates: &fakeTemplateOverlay{byID: map[string][]TemplateClause{
			tID: {{ID: "x", Title: "Should not leak", Body: "..."}},
		}},
	}

	svc.overlayTemplateClauses(context.Background(), pol, resp)

	if len(resp.Items) != 0 {
		t.Errorf("forked policy should not overlay; got %d clauses", len(resp.Items))
	}
}

func TestService_OverlayTemplateClauses_SkipsWhenPersistedClausesExist(t *testing.T) {
	tID := "salvia.consent_to_treatment"
	tState := "default"
	pol := &PolicyRecord{
		ID:                  uuid.New(),
		ClinicID:            uuid.New(),
		SalviaTemplateID:    &tID,
		SalviaTemplateState: &tState,
	}
	// Caller already had clauses in the DB — overlay must not stomp them.
	resp := &PolicyClauseListResponse{Items: []*PolicyClauseResponse{
		{ID: "real-id", BlockID: "real", Title: "Real clause", Body: "From DB"},
	}}
	svc := &Service{
		templates: &fakeTemplateOverlay{byID: map[string][]TemplateClause{
			tID: {{ID: "yaml", Title: "From YAML", Body: "..."}},
		}},
	}

	svc.overlayTemplateClauses(context.Background(), pol, resp)

	if len(resp.Items) != 1 || resp.Items[0].Title != "Real clause" {
		t.Errorf("overlay leaked over persisted clauses: %+v", resp.Items)
	}
}

func TestService_OverlayTemplateClauses_SkipsNonSalviaPolicy(t *testing.T) {
	pol := &PolicyRecord{
		ID:       uuid.New(),
		ClinicID: uuid.New(),
		// SalviaTemplateID nil — pure clinic-authored policy.
	}
	resp := &PolicyClauseListResponse{Items: []*PolicyClauseResponse{}}
	svc := &Service{
		templates: &fakeTemplateOverlay{byID: map[string][]TemplateClause{
			"x": {{ID: "x", Title: "Should not load", Body: "..."}},
		}},
	}

	svc.overlayTemplateClauses(context.Background(), pol, resp)

	if len(resp.Items) != 0 {
		t.Error("non-salvia policy should never trigger overlay")
	}
}
