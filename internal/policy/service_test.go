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

// ── Salvia fork-flag regression ─────────────────────────────────────────────
//
// The bug: salvia_template_state shipped as a flag that was supposed to
// flip from "default" → "forked" on first content mutation, but no code
// ever wrote "forked". The overlay code in service.go gated on
// state=="default" as "clinic hasn't edited yet — paint YAML clauses",
// which meant the YAML overlay kept firing forever and lied about clause
// counts in computeClauseCount, repainted deleted clauses in
// ListClauses, etc.
//
// The fix is two-pronged:
//   1. UpdateDraft / UpsertClauses / PublishPolicy all call
//      repo.MarkPolicyForked at the end so the flag actually transitions.
//   2. computeClauseCount now treats DB count as authoritative whenever
//      a version row exists; the YAML fallback only fires when the DB
//      truly has zero clauses AND the row is still in "default" state.
//
// (Full UpdateDraft → flip → re-read round-trip is exercised in
// integration tests since policy.Service uses a concrete *Repository.
// Here we pin the overlay-gate behaviour that the flag drives.)

func TestRegression_OverlayTemplateClauses_ForkedStateSilencesOverlay(t *testing.T) {
	tID := "salvia.consent_to_treatment"
	tForked := "forked"
	pol := &PolicyRecord{
		ID:                  uuid.New(),
		ClinicID:            uuid.New(),
		SalviaTemplateID:    &tID,
		SalviaTemplateState: &tForked, // flipped by service after first mutation
	}
	// Empty persisted clauses — clinic deleted them on purpose.
	resp := &PolicyClauseListResponse{Items: []*PolicyClauseResponse{}}
	svc := &Service{
		templates: &fakeTemplateOverlay{byID: map[string][]TemplateClause{
			tID: {
				{ID: "scope", Title: "Scope", Body: "..."},
				{ID: "withdrawal", Title: "Withdrawal", Body: "..."},
			},
		}},
	}

	svc.overlayTemplateClauses(context.Background(), pol, resp)

	if len(resp.Items) != 0 {
		t.Errorf("BUG: overlay repainted YAML clauses on a forked row. "+
			"State flip from 'default' to 'forked' is the signal that the "+
			"clinic has authored their own content (including a deliberate "+
			"empty state). Got %d items, want 0.", len(resp.Items))
	}
}

func TestRegression_ComputeClauseCount_DefaultStateNoVersionFallsBackToYAML(t *testing.T) {
	// Freshly installed Salvia policy with no draft/published yet.
	// computeClauseCount should report the YAML overlay count so the
	// card pill matches what the preview drawer renders. This pins the
	// "default state + no DB version" path that the fix preserved.
	tID := "salvia.consent_to_treatment"
	tDefault := "default"
	pol := &PolicyRecord{
		ID:                  uuid.New(),
		ClinicID:            uuid.New(),
		SalviaTemplateID:    &tID,
		SalviaTemplateState: &tDefault,
	}
	svc := &Service{
		templates: &fakeTemplateOverlay{byID: map[string][]TemplateClause{
			tID: {
				{ID: "a", Title: "A", Body: "..."},
				{ID: "b", Title: "B", Body: "..."},
				{ID: "c", Title: "C", Body: "..."},
			},
		}},
	}

	// latest == nil, draft == nil → no DB version → dbCount stays 0 →
	// YAML overlay fires.
	got := svc.computeClauseCount(context.Background(), pol, nil, nil)
	if got != 3 {
		t.Errorf("default-state Salvia row with no DB version should report YAML count, got %d want 3", got)
	}
}

func TestRegression_ComputeClauseCount_ForkedStateSuppressesYAML(t *testing.T) {
	// Once the flag is forked, the YAML fallback must not fire even when
	// the DB has zero clauses — that's the "clinic deliberately emptied"
	// case, where reappearing YAML is exactly the bug we fixed.
	tID := "salvia.consent_to_treatment"
	tForked := "forked"
	pol := &PolicyRecord{
		ID:                  uuid.New(),
		ClinicID:            uuid.New(),
		SalviaTemplateID:    &tID,
		SalviaTemplateState: &tForked,
	}
	svc := &Service{
		templates: &fakeTemplateOverlay{byID: map[string][]TemplateClause{
			tID: {{ID: "a", Title: "A", Body: "..."}},
		}},
	}

	got := svc.computeClauseCount(context.Background(), pol, nil, nil)
	if got != 0 {
		t.Errorf("forked Salvia row with no DB clauses must NOT fall back to YAML, got %d", got)
	}
}
