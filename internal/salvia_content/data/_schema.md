# Content Format Specification

This document is the schema for `*.yaml` files in `salvia_content/`. The Go
backend loader will validate every file against this spec at boot. Files that
do not validate are rejected at startup (no silent skips — the build fails).

---

## File location → vertical mapping

| Path | Vertical | Installed when |
|---|---|---|
| `shared/forms/*.yaml` | (any) | Always — every clinic |
| `shared/policies/*.yaml` | (any) | Always — every clinic |
| `veterinary/forms/*.yaml` | veterinary | Clinic vertical = veterinary |
| `veterinary/policies/*.yaml` | veterinary | Clinic vertical = veterinary |
| `dental/forms/*.yaml` | dental | Clinic vertical = dental |
| `dental/policies/*.yaml` | dental | Clinic vertical = dental |
| `general_clinic/forms/*.yaml` | general_clinic | Clinic vertical = general_clinic |
| `general_clinic/policies/*.yaml` | general_clinic | Clinic vertical = general_clinic |
| `aged_care/forms/*.yaml` | aged_care | Clinic vertical = aged_care |
| `aged_care/policies/*.yaml` | aged_care | Clinic vertical = aged_care |

---

## FORM FILE schema

```yaml
# REQUIRED FIELDS
id: salvia.<vertical_or_shared>.<slug>     # globally unique; dot-separated; lower_snake
name: "Display name"                       # what clinic sees in form list
version: 1                                 # bump on any content change
currency_date: 2026-05-08                  # YYYY-MM-DD; date framework last reviewed
vertical: shared | veterinary | dental | general_clinic | aged_care
countries: [NZ, AU, UK, US]                # which countries get this form (subset OK)

# RECOMMENDED FIELDS
description: >
  One-paragraph plain-English purpose. Shown as form subtitle.
purpose_per_regulator:                     # framework citation per country
  NZ: "HIPC 2020 Rule 1; HDC Code Right 7"
  AU: "RACGP C1.3; APP 3"
  UK: "GMC Decision-making and consent 2020; CQC Reg 11"
  US: "HIPAA + state law"

# OPTIONAL: defaults for the form-policy linker
linked_policy_defaults:
  - salvia.shared.privacy_policy
  - salvia.shared.records_retention_policy

# OPTIONAL: form-level prompt for the AI extractor
overall_prompt: |
  Extract the patient encounter narrative into the structured fields.
  Use clinician's voice. Don't infer beyond what was stated.

# OPTIONAL: tags for grouping/filtering
tags: [consent, intake]

# REQUIRED: at least one field
fields:
  - key: <snake_case_unique_within_form>
    label: "Field display label"
    type: text | long_text | number | decimal | slider | select | button_group
        | percentage | blocks | image | date
        | system.consent | system.drug_op | system.incident | system.pain_score
        | system_field                    # pulled from patient record, never extracted
    required: true | false                 # default false
    ai_extract: true | false               # whether AI extractor populates from transcript
    pii: true | false                      # mask in lists, route reveals through unmask endpoint
    phi: true | false                      # mask by default
    help_text: "Tooltip / clinician guidance"

    # type-specific config (optional unless type requires it)
    config:
      # for select / button_group:
      options:
        - {label: "Yes", value: "yes"}
        - {label: "No", value: "no"}
      # for slider / percentage:
      min: 0
      max: 10
      step: 1
      # for system.consent:
      consent_type: audio_recording | ai_processing | telemedicine | sedation
                  | euthanasia | invasive_procedure | mhr_write | photography
                  | data_sharing | controlled_drug_administration | treatment_plan
                  | other
      require_witness: true | false
      # for system.drug_op:
      operation: administer | dispense | discard | receive | transfer | adjust
      controlled_only: true | false
      confirm_required: true | false
      # for system.incident:
      incident_type: <see incidents module enum>
      min_severity: low | medium | high | critical
      # for system.pain_score:
      scale_id: nrs | flacc | painad | wong_baker | vrs | vas
      # for system_field:
      source: patient_record.<field>       # e.g. patient_record.full_name
                                          # patient_record.dob, patient_record.weight_kg

    # OPTIONAL: country variants of this field
    countries: [NZ, AU]                    # if omitted, field shows for all countries declared on the form
    label_per_country:                     # label override per country
      US: "Insurance / payer information"
    required_per_country:
      US: true                             # required only in US
```

### System field examples

For patient/resident demographic fields that come from the record, never AI:

```yaml
- key: subject_ref
  label: Patient
  type: system_field
  source: patient_record
  required: true

- key: subject_dob
  label: Date of birth
  type: system_field
  source: patient_record.date_of_birth

- key: subject_weight
  label: Weight (kg)
  type: system_field
  source: patient_record.weight_kg
```

The loader populates these as references to the patient record at form-render
time; AI extractor never touches them (per `feedback_system_fields.md`).

---

## POLICY FILE schema

```yaml
# REQUIRED
id: salvia.<vertical_or_shared>.<slug>
name: "Display name"
version: 1
currency_date: 2026-05-08
vertical: shared | veterinary | dental | general_clinic | aged_care
countries: [NZ, AU, UK, US]

# RECOMMENDED
description: >
  Plain-English summary of the policy's scope and purpose.
purpose_per_regulator:
  NZ: "..."
  AU: "..."
  UK: "..."
  US: "..."

# REQUIRED: at least one clause
clauses:
  - id: scope                              # snake_case unique within policy
    title: "Scope"
    body: |                                # if shared text across all countries
      This policy applies to ...
    # OR per-country variation:
    body_per_country:
      NZ: |
        This policy applies to ... under HIPC 2020 ...
      AU: |
        This policy applies to ... under APP 1 ...
      UK: |
        ...
      US: |
        ...

  - id: responsibilities
    title: "Responsibilities"
    body: |
      ...
```

## Cross-cutting required fields on every file

```yaml
badge: "Made by Salvia v1"           # constant; CI enforces
disclaimer: standard                 # references _terms.md
maintainer: "Salvia"                 # for now constant; could become per-vertical reviewer name
```

These are auto-injected by the loader if absent; including them in the file
is encouraged for clarity but not required.

## Validation rules

The loader enforces:

1. `id` is unique across the entire tree.
2. `id` matches `salvia.<shared|veterinary|dental|general_clinic|aged_care>.<slug>`.
3. `vertical` matches the parent folder.
4. `countries` is a non-empty subset of `[NZ, AU, UK, US]`.
5. `currency_date` is within 12 months of build time (warning, not error, in dev).
6. Every form has ≥1 field. Every policy has ≥1 clause.
7. Field `type` is one of the schema enum (mirrors `forms/schema/registry.go`).
8. System widget configs validate against their respective enums (consent type,
   drug op, incident type, pain scale).
9. `system.drug_op` with `operation: administer | dispense` cannot have
   `confirm_required: false` — mirrors backend rule.
10. `linked_policy_defaults` references must resolve (every id must exist in
    the tree).
11. `body_per_country` keys must be a subset of the file's `countries`.

## Loader contract (Go side, not yet implemented)

```go
// pkg: internal/salvia_content
package salvia_content

type Template struct {
    ID                string
    Kind              string  // "form" or "policy"
    Vertical          string
    Countries         []string
    Version           int
    CurrencyDate      time.Time
    Body              []byte  // marshalled form/policy spec
}

func LoadAll(fs fs.FS) ([]Template, error)

func TemplatesFor(vertical, country string) []Template
```

Clinic-create flow calls `TemplatesFor(clinic.vertical, clinic.country)` and
materialises each Template into the appropriate domain table (`forms` /
`policies`) with `salvia_template_id` set, `salvia_template_state = "default"`.
