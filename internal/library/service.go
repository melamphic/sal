// Package library implements the Salvia template library — a browseable
// catalogue of Salvia-authored forms and policies that clinics can preview
// and selectively import. Templates are read from the embedded YAML content
// at runtime (no extra tables); import status is checked against existing
// forms / policies rows via the interfaces wired in app.go.
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/forms"
	"github.com/melamphic/sal/internal/policy"
	"github.com/melamphic/sal/internal/salvia_content"
)

// ── Dependency interfaces ─────────────────────────────────────────────────────

// ClinicLookup fetches the clinic's vertical and ISO-3166 country code.
type ClinicLookup interface {
	GetVerticalAndCountry(ctx context.Context, clinicID uuid.UUID) (vertical, country string, err error)
}

// FormStatusLookup checks whether a Salvia template has been imported by a clinic.
type FormStatusLookup interface {
	// Returns (formID, state, nil) when a row exists, (nil, "", nil) when none.
	GetFormIDForTemplate(ctx context.Context, templateID string, clinicID uuid.UUID) (*uuid.UUID, string, error)
}

// PolicyStatusLookup checks whether a Salvia policy template has been imported.
type PolicyStatusLookup interface {
	// Returns (policyID, state, nil) when a row exists, (nil, "", nil) when none.
	GetPolicyIDForTemplate(ctx context.Context, templateID string, clinicID uuid.UUID) (*uuid.UUID, string, error)
}

// FormImporter creates and updates form drafts on behalf of the library.
type FormImporter interface {
	CreateForm(ctx context.Context, input forms.CreateFormInput) (*forms.FormResponse, error)
	UpdateDraft(ctx context.Context, input forms.UpdateDraftInput) (*forms.FormResponse, error)
}

// PolicyImporter creates policies and writes clauses on behalf of the library.
type PolicyImporter interface {
	CreatePolicy(ctx context.Context, input policy.CreatePolicyInput) (*policy.PolicyResponse, error)
	GetDraftVersionID(ctx context.Context, policyID, clinicID uuid.UUID) (uuid.UUID, error)
	UpsertClauses(ctx context.Context, input policy.UpsertClausesInput) (*policy.PolicyClauseListResponse, error)
}

// ── Response types ────────────────────────────────────────────────────────────

// LibraryTemplateItem is a single card in the library browse list.
//
//nolint:revive
type LibraryTemplateItem struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Kind        string   `json:"kind"` // "form" | "policy"
	Vertical    string   `json:"vertical"`
	Version     int      `json:"version"`
	FieldCount  int      `json:"field_count,omitempty"`
	ClauseCount int      `json:"clause_count,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// LinkedPolicyDefaults lists recommended policy template IDs for forms.
	LinkedPolicyDefaults []string `json:"linked_policy_defaults,omitempty"`
	// ImportStatus is "not_imported" | "imported" | "retired".
	ImportStatus string `json:"import_status"`
	// ImportedID is non-empty when ImportStatus == "imported".
	ImportedID *string `json:"imported_id,omitempty"`
}

// LibraryListResponse is a list of library templates.
//
//nolint:revive
type LibraryListResponse struct {
	Items []*LibraryTemplateItem `json:"items"`
	Total int                    `json:"total"`
}

// LibraryFieldPreview is a lightweight field summary for the detail drawer.
//
//nolint:revive
type LibraryFieldPreview struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

// LibraryClausePreview is a lightweight clause summary for the detail drawer.
//
//nolint:revive
type LibraryClausePreview struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Parity string `json:"parity,omitempty"`
}

// LibraryTemplateDetail is the full template detail shown in the preview drawer.
//
//nolint:revive
type LibraryTemplateDetail struct {
	LibraryTemplateItem
	OverallPrompt string                  `json:"overall_prompt,omitempty"`
	Fields        []*LibraryFieldPreview  `json:"fields,omitempty"`
	Clauses       []*LibraryClausePreview `json:"clauses,omitempty"`
}

// LibraryImportResponse is returned after a successful import.
//
//nolint:revive
type LibraryImportResponse struct {
	// ID is the form or policy UUID that was created / promoted.
	ID string `json:"id"`
	// AlreadyImported is true when the template was already forked —
	// no action taken, existing ID returned.
	AlreadyImported bool `json:"already_imported"`
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service implements the library read + import logic.
type Service struct {
	mat          *salvia_content.Materialiser
	clinic       ClinicLookup
	formStatus   FormStatusLookup
	policyStatus PolicyStatusLookup
	forms        FormImporter
	policies     PolicyImporter
}

// NewService constructs a library Service.
func NewService(
	mat *salvia_content.Materialiser,
	clinic ClinicLookup,
	formStatus FormStatusLookup,
	policyStatus PolicyStatusLookup,
	forms FormImporter,
	policies PolicyImporter,
) *Service {
	return &Service{
		mat:          mat,
		clinic:       clinic,
		formStatus:   formStatus,
		policyStatus: policyStatus,
		forms:        forms,
		policies:     policies,
	}
}

// ListForms returns all Salvia form templates available for this clinic's
// (vertical, country) pair, each annotated with its import status.
func (s *Service) ListForms(ctx context.Context, clinicID uuid.UUID) (*LibraryListResponse, error) {
	vertical, country, err := s.clinic.GetVerticalAndCountry(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.ListForms: %w", err)
	}
	c := salvia_content.Country(strings.ToUpper(country))
	templates := s.applicableTemplates(vertical, country, salvia_content.KindForm)

	items := make([]*LibraryTemplateItem, 0, len(templates))
	for _, t := range templates {
		item, err := s.toFormItem(ctx, t, c, clinicID)
		if err != nil {
			return nil, fmt.Errorf("library.service.ListForms: %w", err)
		}
		items = append(items, item)
	}
	return &LibraryListResponse{Items: items, Total: len(items)}, nil
}

// ListPolicies returns all Salvia policy templates available for this clinic.
func (s *Service) ListPolicies(ctx context.Context, clinicID uuid.UUID) (*LibraryListResponse, error) {
	vertical, country, err := s.clinic.GetVerticalAndCountry(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.ListPolicies: %w", err)
	}
	c := salvia_content.Country(strings.ToUpper(country))
	templates := s.applicableTemplates(vertical, country, salvia_content.KindPolicy)

	items := make([]*LibraryTemplateItem, 0, len(templates))
	for _, t := range templates {
		item, err := s.toPolicyItem(ctx, t, c, clinicID)
		if err != nil {
			return nil, fmt.Errorf("library.service.ListPolicies: %w", err)
		}
		items = append(items, item)
	}
	return &LibraryListResponse{Items: items, Total: len(items)}, nil
}

// GetForm returns the full detail for a form template, including field previews.
func (s *Service) GetForm(ctx context.Context, templateID string, clinicID uuid.UUID) (*LibraryTemplateDetail, error) {
	t, ok := s.mat.TemplateByID(templateID)
	if !ok || t.Kind != salvia_content.KindForm {
		return nil, fmt.Errorf("library.service.GetForm: %w", domain.ErrNotFound)
	}

	_, country, err := s.clinic.GetVerticalAndCountry(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.GetForm: %w", err)
	}
	c := salvia_content.Country(strings.ToUpper(country))

	item, err := s.toFormItem(ctx, t, c, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.GetForm: %w", err)
	}

	fields := make([]*LibraryFieldPreview, 0, len(t.Fields))
	for _, f := range t.Fields {
		if !f.AppliesToCountry(c) {
			continue
		}
		fields = append(fields, &LibraryFieldPreview{
			Key:      f.Key,
			Label:    f.LabelFor(c),
			Type:     f.Type,
			Required: f.IsRequiredFor(c),
		})
	}

	return &LibraryTemplateDetail{
		LibraryTemplateItem: *item,
		OverallPrompt:       t.OverallPrompt,
		Fields:              fields,
	}, nil
}

// GetPolicy returns the full detail for a policy template, including clause previews.
func (s *Service) GetPolicy(ctx context.Context, templateID string, clinicID uuid.UUID) (*LibraryTemplateDetail, error) {
	t, ok := s.mat.TemplateByID(templateID)
	if !ok || t.Kind != salvia_content.KindPolicy {
		return nil, fmt.Errorf("library.service.GetPolicy: %w", domain.ErrNotFound)
	}

	_, country, err := s.clinic.GetVerticalAndCountry(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.GetPolicy: %w", err)
	}
	c := salvia_content.Country(strings.ToUpper(country))

	item, err := s.toPolicyItem(ctx, t, c, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.GetPolicy: %w", err)
	}

	clauses := make([]*LibraryClausePreview, 0, len(t.Clauses))
	for _, cl := range t.Clauses {
		clauses = append(clauses, &LibraryClausePreview{
			ID:     cl.ID,
			Title:  cl.Title,
			Body:   cl.BodyFor(c),
			Parity: cl.Parity,
		})
	}

	return &LibraryTemplateDetail{
		LibraryTemplateItem: *item,
		Clauses:             clauses,
	}, nil
}

// ImportForm promotes a Salvia form template into the clinic's active draft list.
// Idempotent: if already forked, returns the existing form ID without changes.
func (s *Service) ImportForm(ctx context.Context, templateID string, clinicID, staffID uuid.UUID) (*LibraryImportResponse, error) {
	t, ok := s.mat.TemplateByID(templateID)
	if !ok || t.Kind != salvia_content.KindForm {
		return nil, fmt.Errorf("library.service.ImportForm: %w", domain.ErrNotFound)
	}

	_, country, err := s.clinic.GetVerticalAndCountry(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.ImportForm: %w", err)
	}
	c := salvia_content.Country(strings.ToUpper(country))

	formID, state, err := s.formStatus.GetFormIDForTemplate(ctx, templateID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.ImportForm: status: %w", err)
	}

	if state == string(salvia_content.StateForked) {
		idStr := formID.String()
		return &LibraryImportResponse{ID: idStr, AlreadyImported: true}, nil
	}
	if state == string(salvia_content.StateDeleted) {
		return nil, fmt.Errorf("library.service.ImportForm: template retired by this clinic: %w", domain.ErrConflict)
	}

	// state == "default" (materialiser created a light row) or no row yet.
	var targetFormID uuid.UUID
	if formID != nil {
		targetFormID = *formID
	} else {
		cd := t.CurrencyDate()
		resp, err := s.forms.CreateForm(ctx, forms.CreateFormInput{
			ClinicID:              clinicID,
			StaffID:               staffID,
			Name:                  t.Name,
			Description:           optStr(t.Description),
			Tags:                  t.Tags,
			SalviaTemplateID:      strPtr(t.ID),
			SalviaTemplateVersion: intPtr(t.Version),
			SalviaTemplateState:   strPtr(string(salvia_content.StateDefault)),
			FrameworkCurrencyDate: nonZeroTime(cd),
		})
		if err != nil {
			return nil, fmt.Errorf("library.service.ImportForm: create: %w", err)
		}
		id, err := uuid.Parse(resp.ID)
		if err != nil {
			return nil, fmt.Errorf("library.service.ImportForm: parse id: %w", err)
		}
		targetFormID = id
	}

	// UpdateDraft writes YAML fields and auto-marks the row as forked.
	resp, err := s.forms.UpdateDraft(ctx, forms.UpdateDraftInput{
		FormID:        targetFormID,
		ClinicID:      clinicID,
		StaffID:       staffID,
		Name:          t.Name,
		Description:   optStr(t.Description),
		OverallPrompt: optStr(t.OverallPrompt),
		Tags:          t.Tags,
		Fields:        yamlFieldsToInput(t, c),
	})
	if err != nil {
		return nil, fmt.Errorf("library.service.ImportForm: update draft: %w", err)
	}

	return &LibraryImportResponse{ID: resp.ID}, nil
}

// ImportPolicy promotes a Salvia policy template into the clinic's active policy list.
func (s *Service) ImportPolicy(ctx context.Context, templateID string, clinicID, staffID uuid.UUID) (*LibraryImportResponse, error) {
	t, ok := s.mat.TemplateByID(templateID)
	if !ok || t.Kind != salvia_content.KindPolicy {
		return nil, fmt.Errorf("library.service.ImportPolicy: %w", domain.ErrNotFound)
	}

	_, country, err := s.clinic.GetVerticalAndCountry(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.ImportPolicy: %w", err)
	}
	c := salvia_content.Country(strings.ToUpper(country))

	polID, state, err := s.policyStatus.GetPolicyIDForTemplate(ctx, templateID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.ImportPolicy: status: %w", err)
	}

	if state == string(salvia_content.StateForked) {
		idStr := polID.String()
		return &LibraryImportResponse{ID: idStr, AlreadyImported: true}, nil
	}
	if state == string(salvia_content.StateDeleted) {
		return nil, fmt.Errorf("library.service.ImportPolicy: template retired by this clinic: %w", domain.ErrConflict)
	}

	var targetPolID uuid.UUID
	if polID != nil {
		targetPolID = *polID
	} else {
		cd := t.CurrencyDate()
		resp, err := s.policies.CreatePolicy(ctx, policy.CreatePolicyInput{
			ClinicID:              clinicID,
			StaffID:               staffID,
			Name:                  t.Name,
			Description:           optStr(t.Description),
			SalviaTemplateID:      strPtr(t.ID),
			SalviaTemplateVersion: intPtr(t.Version),
			SalviaTemplateState:   strPtr(string(salvia_content.StateDefault)),
			FrameworkCurrencyDate: nonZeroTime(cd),
		})
		if err != nil {
			return nil, fmt.Errorf("library.service.ImportPolicy: create: %w", err)
		}
		id, err := uuid.Parse(resp.ID)
		if err != nil {
			return nil, fmt.Errorf("library.service.ImportPolicy: parse id: %w", err)
		}
		targetPolID = id
	}

	draftVersionID, err := s.policies.GetDraftVersionID(ctx, targetPolID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("library.service.ImportPolicy: get draft: %w", err)
	}

	if _, err := s.policies.UpsertClauses(ctx, policy.UpsertClausesInput{
		PolicyID:  targetPolID,
		ClinicID:  clinicID,
		VersionID: draftVersionID,
		Clauses:   yamlClausesToInput(t, c),
	}); err != nil {
		return nil, fmt.Errorf("library.service.ImportPolicy: upsert clauses: %w", err)
	}

	return &LibraryImportResponse{ID: targetPolID.String()}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (s *Service) applicableTemplates(vertical, country string, kind salvia_content.Kind) []salvia_content.Template {
	all := s.mat.AllTemplates()
	v := salvia_content.Vertical(vertical)
	c := salvia_content.Country(strings.ToUpper(country))

	out := make([]salvia_content.Template, 0)
	for _, t := range all {
		if t.Kind == kind && t.AppliesTo(v, c) {
			out = append(out, t)
		}
	}
	return out
}

func (s *Service) toFormItem(ctx context.Context, t salvia_content.Template, c salvia_content.Country, clinicID uuid.UUID) (*LibraryTemplateItem, error) {
	status, importedID, err := s.formImportStatus(ctx, t.ID, clinicID)
	if err != nil {
		return nil, err
	}
	fieldCount := 0
	for _, f := range t.Fields {
		if f.AppliesToCountry(c) {
			fieldCount++
		}
	}
	return &LibraryTemplateItem{
		ID:                   t.ID,
		Name:                 t.Name,
		Description:          t.Description,
		Kind:                 string(salvia_content.KindForm),
		Vertical:             string(t.Vertical),
		Version:              t.Version,
		FieldCount:           fieldCount,
		Tags:                 t.Tags,
		LinkedPolicyDefaults: t.LinkedPolicyDefaults,
		ImportStatus:         status,
		ImportedID:           importedID,
	}, nil
}

func (s *Service) toPolicyItem(ctx context.Context, t salvia_content.Template, c salvia_content.Country, clinicID uuid.UUID) (*LibraryTemplateItem, error) {
	_ = c
	status, importedID, err := s.policyImportStatus(ctx, t.ID, clinicID)
	if err != nil {
		return nil, err
	}
	return &LibraryTemplateItem{
		ID:           t.ID,
		Name:         t.Name,
		Description:  t.Description,
		Kind:         string(salvia_content.KindPolicy),
		Vertical:     string(t.Vertical),
		Version:      t.Version,
		ClauseCount:  len(t.Clauses),
		Tags:         t.Tags,
		ImportStatus: status,
		ImportedID:   importedID,
	}, nil
}

func (s *Service) formImportStatus(ctx context.Context, templateID string, clinicID uuid.UUID) (status string, importedID *string, err error) {
	id, state, err := s.formStatus.GetFormIDForTemplate(ctx, templateID, clinicID)
	if err != nil {
		return "", nil, err
	}
	switch state {
	case string(salvia_content.StateForked):
		idStr := id.String()
		return "imported", &idStr, nil
	case string(salvia_content.StateDeleted):
		return "retired", nil, nil
	default:
		return "not_imported", nil, nil
	}
}

func (s *Service) policyImportStatus(ctx context.Context, templateID string, clinicID uuid.UUID) (status string, importedID *string, err error) {
	id, state, err := s.policyStatus.GetPolicyIDForTemplate(ctx, templateID, clinicID)
	if err != nil {
		return "", nil, err
	}
	switch state {
	case string(salvia_content.StateForked):
		idStr := id.String()
		return "imported", &idStr, nil
	case string(salvia_content.StateDeleted):
		return "retired", nil, nil
	default:
		return "not_imported", nil, nil
	}
}

// yamlFieldsToInput converts YAML FieldSpec to forms.FieldInput, filtered by
// country and excluding system_field types (patient fields are never draft fields).
func yamlFieldsToInput(t salvia_content.Template, c salvia_content.Country) []forms.FieldInput {
	out := make([]forms.FieldInput, 0, len(t.Fields))
	pos := 1
	for _, f := range t.Fields {
		if !f.AppliesToCountry(c) || f.Type == "system_field" {
			continue
		}
		var cfg json.RawMessage
		if len(f.Config) > 0 {
			if b, err := json.Marshal(f.Config); err == nil {
				cfg = b
			}
		}
		out = append(out, forms.FieldInput{
			Position:       pos,
			Title:          f.LabelFor(c),
			Type:           f.Type,
			Config:         cfg,
			Required:       f.IsRequiredFor(c),
			AllowInference: true,
		})
		pos++
	}
	return out
}

// yamlClausesToInput converts YAML ClauseSpec to policy.ClauseItemInput.
func yamlClausesToInput(t salvia_content.Template, c salvia_content.Country) []policy.ClauseItemInput {
	out := make([]policy.ClauseItemInput, 0, len(t.Clauses))
	for _, cl := range t.Clauses {
		out = append(out, policy.ClauseItemInput{
			BlockID: cl.ID,
			Title:   cl.Title,
			Body:    cl.BodyFor(c),
			Parity:  cl.Parity,
		})
	}
	return out
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nonZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
