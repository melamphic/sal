# Consent & Pain

Two universal modules — every clinic across all 16 (vertical × country)
combos uses both. Consent captures verbal / written / electronic /
guardian sign-offs for procedures, sedation, photography, telemedicine,
data sharing, etc. Pain captures 0–10 scores using the right scale for
the population (NRS, FLACC, PainAD, Wong-Baker, VRS, VAS).

---

## Consent

### Capture methods

| `captured_via` | When to use |
|---|---|
| `verbal_clinic` | In-clinic verbal consent. **Witness staff_id required** (DB-level CHECK + service-level validation). |
| `verbal_telehealth` | Verbal consent over video. No witness required. |
| `written_signature` | Paper signature, scanned + uploaded. `signature_image_key` points at storage. |
| `electronic_signature` | Signature pad / portal sig. |
| `guardian` | Captured from authorised representative (parent, EPOA, NOK). `consenting_party_relationship` records who. |

### Consent types

12 enum values; the schema (`00056_create_consent_records.sql`) enforces
the full list. Highlights: `audio_recording`, `ai_processing`,
`telemedicine`, `sedation`, `euthanasia` (vet), `invasive_procedure`,
`photography`, `data_sharing`, `controlled_drug_administration`,
`treatment_plan`, `other`.

### Per-type expiry defaults

Server applies defaults at capture time when the caller didn't pass
`expires_at`. Clinics override by passing an explicit value.

| Consent type | Default expiry |
|---|---|
| `audio_recording` · `ai_processing` · `telemedicine` · `data_sharing` | 365 days |
| `mhr_write` | 365 days (NZ Health Records) |
| `photography` | 730 days |
| `treatment_plan` | 180 days |
| `sedation` · `euthanasia` · `invasive_procedure` · `controlled_drug_administration` | none — tied to a single event |

`renewal_due_at` is independent: a consent can be valid AND due for
re-affirmation review. Aged-care MHR consents typically set
`renewal_due_at` to 12 months even though `expires_at` is also 12 months
— the workflow flags it earlier.

### Withdrawal

Append-only against the row. `POST /api/v1/consent/{id}/withdraw` with
a documented reason stamps `withdrawal_at` + `withdrawal_reason` and
nothing else. The original capture metadata stays intact for audit.

### AI drafting (C3)

`POST /api/v1/consent/ai-draft` returns AI-drafted `risks_discussed`
and `alternatives_discussed` text given a procedure description. The
clinician reviews and edits before submitting via the regular capture
endpoint. Salvia **never** auto-saves consent text — this endpoint
is a drafting aid only. AI metadata (provider, model, prompt hash) is
returned so the UI can render an "AI drafted" badge if the values
survive into the captured record.

### API surface

```
POST   /api/v1/consent                         (manage_patients)
GET    /api/v1/consent                         (view ∪ manage)
GET    /api/v1/consent/{id}                    (view ∪ manage)
PATCH  /api/v1/consent/{id}                    (manage_patients · limited fields)
POST   /api/v1/consent/{id}/withdraw           (manage_patients)
POST   /api/v1/consent/ai-draft                (manage_patients · AI provider only)
```

List endpoint supports `subject_id`, `consent_type`, `only_active`
(excludes withdrawn + expired), and `expiring_within=720h` (30 days)
filters. The expiring-soon set powers the admin alerts endpoint.

---

## Pain

### Scales (per the migration constraint)

| Scale | When to use |
|---|---|
| `nrs` | 0–10 Numeric Rating Scale — universal, default. |
| `flacc` | Face / Legs / Activity / Cry / Consolability — non-verbal patients (paediatric, severe cognitive impairment, post-anaesthesia). |
| `painad` | Pain Assessment in Advanced Dementia — aged-care default for residents with dementia. |
| `wong_baker` | Wong-Baker FACES — paediatric verbal patients. |
| `vrs` | Verbal Rating Scale (none / mild / moderate / severe) mapped to 0–10. |
| `vas` | Visual Analog Scale — common in dental for procedure pain. |

### Per-vertical recommendation

`pain.RecommendedScale(vertical, country)` returns the most common
scale for the (vertical, country) pair. Used as the default in the
"record pain" UI. Easily extensible.

```go
func RecommendedScale(vertical, country string) string {
    switch vertical {
    case "aged_care":             return "painad"  // dementia is the modal case
    case "vet", "veterinary":     return "nrs"
    case "dental":                return "vas"
    case "general", "general_clinic": return "nrs"
    }
    return "nrs"
}
```

### Method (input modality)

| `method` | Source |
|---|---|
| `manual` | Clinician sets the score directly. |
| `painchek` | Future PainChek facial-assessment integration. |
| `extracted_from_audio` | Score harvested from encounter transcript via the existing notes extraction pipeline. |
| `flacc_observed` · `wong_baker` | Score recorded against the named scale by direct observation. |

### Trend endpoint

`GET /api/v1/pain-scores/subjects/{id}/trend?since=…&until=…` returns
count, mean, latest, peak. Defaults to last 30 days. Used by the
patient hub trend chip and by the (deferred) pre-encounter brief AI
flow.

### API surface

```
POST   /api/v1/pain-scores                      (manage_patients ∪ dispense)
GET    /api/v1/pain-scores                      (view ∪ manage)
GET    /api/v1/pain-scores/{id}                 (view ∪ manage)
GET    /api/v1/pain-scores/subjects/{id}/trend  (view ∪ manage)
```

`dispense` is included on the record permission because nurses /
caregivers in aged care already carry it for the medication path —
pain scoring is part of the same workflow.
