package domain

// FieldType is the rendering type for a vertical-schema field.
// The frontend uses this to choose an input widget and validation rules
// — e.g. `long_text` maps to a multi-line textarea, `enum` to a dropdown.
type FieldType string

const (
	FieldTypeText     FieldType = "text"
	FieldTypeLongText FieldType = "long_text"
	FieldTypeNumber   FieldType = "number"
	FieldTypeEnum     FieldType = "enum"
	FieldTypeDate     FieldType = "date"
	FieldTypeBoolean  FieldType = "boolean"
)

// FieldDef describes one form field on a per-vertical extension table.
// The Key is the JSON name used on the wire (subject creation/update
// payloads and GET responses), so the frontend can read and write
// values without per-vertical code.
type FieldDef struct {
	Key      string    `json:"key"`
	Label    string    `json:"label"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required"`
	// Options populates dropdowns when Type == FieldTypeEnum.
	Options []string `json:"options,omitempty"`
	// PII flags fields that are personally identifying (insurance number,
	// microchip). The UI masks these by default and routes reveals through
	// the unmask-pii endpoint.
	PII bool `json:"pii"`
	// PHI flags fields that contain protected health information
	// (allergies, medications, chronic conditions). Mask-by-default.
	PHI      bool   `json:"phi"`
	HelpText string `json:"help_text,omitempty"`
}

// VerticalSchema is the full form definition for one vertical — used by
// the frontend to render Create/View/Edit patient forms generically.
type VerticalSchema struct {
	Vertical           Vertical   `json:"vertical"`
	SubjectLabel       string     `json:"subject_label"`
	SubjectLabelPlural string     `json:"subject_label_plural"`
	ContactLabel       string     `json:"contact_label"`
	Fields             []FieldDef `json:"fields"`
}

// VerticalSchemas is the registry of per-vertical form definitions.
// Adding a new vertical is additive — register it here and the generic
// frontend renderer picks it up without code changes elsewhere.
var VerticalSchemas = map[Vertical]VerticalSchema{
	VerticalVeterinary: {
		Vertical:           VerticalVeterinary,
		SubjectLabel:       "Patient",
		SubjectLabelPlural: "Patients",
		ContactLabel:       "Owner",
		Fields: []FieldDef{
			{Key: "species", Label: "Species", Type: FieldTypeEnum, Required: true,
				Options: []string{"dog", "cat", "bird", "rabbit", "reptile", "other"}},
			{Key: "breed", Label: "Breed", Type: FieldTypeText},
			{Key: "sex", Label: "Sex", Type: FieldTypeEnum,
				Options: []string{"male", "female", "unknown"}},
			{Key: "desexed", Label: "Desexed", Type: FieldTypeBoolean},
			{Key: "date_of_birth", Label: "Date of birth", Type: FieldTypeDate},
			{Key: "color", Label: "Color", Type: FieldTypeText},
			{Key: "microchip", Label: "Microchip", Type: FieldTypeText,
				HelpText: "Microchip identifier. Not PII."},
			{Key: "weight_kg", Label: "Weight (kg)", Type: FieldTypeNumber},
			{Key: "allergies", Label: "Allergies", Type: FieldTypeLongText, PHI: true},
			{Key: "chronic_conditions", Label: "Chronic conditions", Type: FieldTypeLongText, PHI: true},
			{Key: "admission_warnings", Label: "Admission warnings", Type: FieldTypeLongText,
				HelpText: "Safety warnings at intake (e.g. aggressive, bite history)."},
			{Key: "insurance_provider_name", Label: "Insurance provider", Type: FieldTypeText},
			{Key: "insurance_policy_number", Label: "Policy number", Type: FieldTypeText, PII: true},
			{Key: "referring_vet_name", Label: "Referring vet", Type: FieldTypeText},
		},
	},
	VerticalDental: {
		Vertical:           VerticalDental,
		SubjectLabel:       "Patient",
		SubjectLabelPlural: "Patients",
		ContactLabel:       "Guardian",
		Fields: []FieldDef{
			{Key: "date_of_birth", Label: "Date of birth", Type: FieldTypeDate, Required: true},
			{Key: "sex", Label: "Sex", Type: FieldTypeEnum,
				Options: []string{"male", "female", "other", "unknown"}},
			{Key: "medical_alerts", Label: "Medical alerts", Type: FieldTypeLongText, PHI: true,
				HelpText: "Conditions flagged for safety (e.g. latex allergy, MRSA)."},
			{Key: "medications", Label: "Current medications", Type: FieldTypeLongText, PHI: true},
			{Key: "allergies", Label: "Allergies", Type: FieldTypeLongText, PHI: true},
			{Key: "chronic_conditions", Label: "Chronic conditions", Type: FieldTypeLongText, PHI: true},
			{Key: "admission_warnings", Label: "Admission warnings", Type: FieldTypeLongText},
			{Key: "insurance_provider_name", Label: "Insurance provider", Type: FieldTypeText},
			{Key: "insurance_policy_number", Label: "Policy number", Type: FieldTypeText, PII: true},
			{Key: "referring_dentist_name", Label: "Referring dentist", Type: FieldTypeText},
			{Key: "primary_dentist_name", Label: "Primary dentist", Type: FieldTypeText},
		},
	},
	VerticalGeneralClinic: {
		Vertical:           VerticalGeneralClinic,
		SubjectLabel:       "Patient",
		SubjectLabelPlural: "Patients",
		ContactLabel:       "Next of kin",
		Fields: []FieldDef{
			{Key: "date_of_birth", Label: "Date of birth", Type: FieldTypeDate, Required: true},
			{Key: "sex", Label: "Sex", Type: FieldTypeEnum,
				Options: []string{"male", "female", "other", "unknown"}},
			{Key: "medical_alerts", Label: "Medical alerts", Type: FieldTypeLongText, PHI: true,
				HelpText: "Conditions flagged for safety (e.g. allergies, MRSA, DNR)."},
			{Key: "medications", Label: "Current medications", Type: FieldTypeLongText, PHI: true},
			{Key: "allergies", Label: "Allergies", Type: FieldTypeLongText, PHI: true},
			{Key: "chronic_conditions", Label: "Chronic conditions", Type: FieldTypeLongText, PHI: true},
			{Key: "admission_warnings", Label: "Admission warnings", Type: FieldTypeLongText},
			{Key: "insurance_provider_name", Label: "Insurance provider", Type: FieldTypeText},
			{Key: "insurance_policy_number", Label: "Policy number", Type: FieldTypeText, PII: true},
			{Key: "referring_provider_name", Label: "Referring provider", Type: FieldTypeText},
			{Key: "primary_provider_name", Label: "Primary provider", Type: FieldTypeText},
		},
	},
	VerticalAgedCare: {
		Vertical:           VerticalAgedCare,
		SubjectLabel:       "Resident",
		SubjectLabelPlural: "Residents",
		ContactLabel:       "Next of kin",
		// Fields wired up when the aged-care extension table lands.
		Fields: []FieldDef{},
	},
}

// SchemaFor returns the schema for a vertical and whether it is registered.
func SchemaFor(v Vertical) (VerticalSchema, bool) {
	s, ok := VerticalSchemas[v]
	return s, ok
}
