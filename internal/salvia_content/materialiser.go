package salvia_content

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/forms"
	"github.com/melamphic/sal/internal/policy"
)

// Materialiser installs the per-(vertical, country) Salvia content library
// into a freshly-created clinic at signup. Each installed form/policy row
// carries lineage columns (`salvia_template_id`, `salvia_template_version`,
// `salvia_template_state`, `framework_currency_date`) that drive the
// "Made by Salvia v1" badge, the Library panel, and the upgrade banner.
//
// V1 design — the materialiser creates LIGHT rows: a forms / policies row
// with name, description, and lineage. It does NOT populate the draft
// fields/clauses inline at clinic-create. The fields/clauses are read from
// the embedded YAML at render-time as long as the row's state stays
// `default`. The clinic forks (state → `forked`) on first edit, at which
// point the YAML content is copied into a real draft and lineage is
// retained for audit only.
//
// This keeps clinic-create fast, avoids the cross-domain fanout into
// form_versions / form_fields / policy_versions / policy_clauses tables,
// and matches the stated lifecycle semantics (default = unchanged from
// upstream).
type Materialiser struct {
	loaded   []Template
	byID     map[string]Template
	forms    FormsService
	policies PolicyService
	logger   *slog.Logger
}

// TemplateByID returns the loaded template with the given ID. The second
// return value reports whether a match was found.
//
// Used by the forms and policy services at render-time: when a row's
// `salvia_template_state` is still `default`, the service overlays the
// embedded YAML fields/clauses onto the response so the clinic sees the
// canonical content without a heavy at-signup write. Once the clinic
// edits the row (state flips to `forked`), the DB draft becomes the
// source of truth and this lookup is bypassed.
func (m *Materialiser) TemplateByID(id string) (Template, bool) {
	t, ok := m.byID[id]
	return t, ok
}

// FormsService is the slice of forms.Service the materialiser consumes.
// Declared here as an interface so the cross-domain dependency is
// minimised and tests can fake it.
type FormsService interface {
	CreateForm(ctx context.Context, input forms.CreateFormInput) (*forms.FormResponse, error)
}

// PolicyService is the slice of policy.Service the materialiser consumes.
type PolicyService interface {
	CreatePolicy(ctx context.Context, input policy.CreatePolicyInput) (*policy.PolicyResponse, error)
}

// NewMaterialiser builds a Materialiser. The templates are loaded once
// from the embedded fs at construction; missing or malformed files cause
// LoadAll to error. Either pass non-nil services for both forms and
// policies, or pass nil to disable that side (useful in tests).
func NewMaterialiser(formsSvc FormsService, polSvc PolicyService, logger *slog.Logger) (*Materialiser, error) {
	all, err := LoadAll()
	if err != nil {
		return nil, fmt.Errorf("salvia_content.NewMaterialiser: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	byID := make(map[string]Template, len(all))
	for _, t := range all {
		byID[t.ID] = t
	}
	return &Materialiser{
		loaded:   all,
		byID:     byID,
		forms:    formsSvc,
		policies: polSvc,
		logger:   logger,
	}, nil
}

// MaterialiseFor installs every applicable template into the supplied clinic.
// Best-effort: a per-template failure is logged and skipped, so a single bad
// row does not abort clinic creation. Returns a Report covering what was
// installed and any failures.
//
// The staffID is the admin-of-record at signup — every materialised row is
// stamped with this staff as `created_by` so the audit trail shows who
// onboarded the clinic. Pass uuid.Nil when called from a system context
// (e.g. seed scripts); the loader rejects nil at the service layer.
func (m *Materialiser) MaterialiseFor(
	ctx context.Context,
	clinicID uuid.UUID,
	vertical domain.Vertical,
	country string,
	staffID uuid.UUID,
) Report {
	v := mapVertical(vertical)
	c := Country(strings.ToUpper(country))
	templates := TemplatesFor(m.loaded, v, c)

	state := string(StateDefault)
	report := Report{
		ClinicID:    clinicID,
		Vertical:    string(vertical),
		Country:     country,
		Templates:   len(templates),
	}

	formTs, polTs := FormsAndPolicies(templates)

	for _, t := range formTs {
		if m.forms == nil {
			report.SkippedNoSvc++
			continue
		}
		_, err := m.forms.CreateForm(ctx, forms.CreateFormInput{
			ClinicID:               clinicID,
			StaffID:                staffID,
			Name:                   t.Name,
			Description:            optString(t.Description),
			Tags:                   t.Tags,
			SalviaTemplateID:       strPtr(t.ID),
			SalviaTemplateVersion:  intPtr(t.Version),
			SalviaTemplateState:    strPtr(state),
			FrameworkCurrencyDate:  timePtr(t.CurrencyDate),
		})
		if err != nil {
			report.FormErrors = append(report.FormErrors, TemplateError{
				ID: t.ID, Path: t.Path, Err: err.Error(),
			})
			m.logger.Warn("salvia_content.MaterialiseFor: form create failed",
				"template_id", t.ID, "clinic_id", clinicID, "err", err)
			continue
		}
		report.FormsCreated++
	}

	for _, t := range polTs {
		if m.policies == nil {
			report.SkippedNoSvc++
			continue
		}
		_, err := m.policies.CreatePolicy(ctx, policy.CreatePolicyInput{
			ClinicID:    clinicID,
			StaffID:     staffID,
			Name:        t.Name,
			Description: optString(t.Description),
			SalviaTemplateID:      strPtr(t.ID),
			SalviaTemplateVersion: intPtr(t.Version),
			SalviaTemplateState:   strPtr(state),
			FrameworkCurrencyDate: timePtr(t.CurrencyDate),
		})
		if err != nil {
			report.PolicyErrors = append(report.PolicyErrors, TemplateError{
				ID: t.ID, Path: t.Path, Err: err.Error(),
			})
			m.logger.Warn("salvia_content.MaterialiseFor: policy create failed",
				"template_id", t.ID, "clinic_id", clinicID, "err", err)
			continue
		}
		report.PoliciesCreated++
	}

	m.logger.Info("salvia_content.MaterialiseFor: complete",
		"clinic_id", clinicID, "vertical", vertical, "country", country,
		"forms", report.FormsCreated, "policies", report.PoliciesCreated,
		"form_errors", len(report.FormErrors),
		"policy_errors", len(report.PolicyErrors))

	return report
}

// Report describes what the materialiser did for one clinic.
type Report struct {
	ClinicID        uuid.UUID
	Vertical        string
	Country         string
	Templates       int             // total templates that applied
	FormsCreated    int
	PoliciesCreated int
	SkippedNoSvc    int             // service was nil; template skipped
	FormErrors      []TemplateError
	PolicyErrors    []TemplateError
}

// TemplateError captures a per-template failure during materialisation.
type TemplateError struct {
	ID   string
	Path string
	Err  string
}

// mapVertical converts a domain.Vertical into the loader's local Vertical.
// Both are aliased to string so this is a one-line cast, but we centralise
// the mapping so future divergence (e.g. a new domain vertical that isn't
// yet in the content tree) can be handled in one place.
func mapVertical(v domain.Vertical) Vertical {
	switch v {
	case domain.VerticalVeterinary:
		return VerticalVeterinary
	case domain.VerticalDental:
		return VerticalDental
	case domain.VerticalGeneralClinic:
		return VerticalGeneralClinic
	case domain.VerticalAgedCare:
		return VerticalAgedCare
	}
	// Unknown vertical — return a sentinel that won't match any template.
	return Vertical("unknown")
}

func strPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// timePtr converts a Template's parsed CurrencyDate function-result into
// the *time.Time the form/policy create input expects. Returns nil for the
// zero time so the column lands as NULL rather than 0001-01-01.
func timePtr(fn func() time.Time) *time.Time {
	v := fn()
	if v.IsZero() {
		return nil
	}
	return &v
}
