# Salvia-Provided Content (v1)

The `salvia_content` package is Salvia's **prebuilt-content track**: a curated
library of forms and policies that ship into every clinic at onboarding,
branded *"Made by Salvia v1"*. Content is country-localised across NZ, AU, UK,
US and per-vertical (veterinary, dental, general clinic, aged care).

This page covers the architecture, lifecycle, and operational expectations.
For the product framing and the disclaimer text shown to users, see
`SALVIA_PROVIDED_CONTENT.md` at the repo root and
`internal/salvia_content/data/_terms.md`.

---

## Why this exists

The marketplace was originally going to be the Day-0 content distribution
play. That has a cold-start problem: a marketplace with no listings is
worse than no marketplace. The prebuilt content track is the answer to
*"how does a brand-new clinic get a working forms library on Day 0?"*

The marketplace is now shelved behind `kMarketplaceEnabled = false` in
`salvia/apps/lib/core/feature_flags.dart` (see `MARKETPLACE_BACKLOG.md`).
Its code, DB tables, and routes remain mounted; only the UI is dormant.

---

## Layout

```
sal/internal/salvia_content/
├── README.md           ← package overview
├── types.go            ← Template, FieldSpec, ClauseSpec, RegulatorMap
├── loader.go           ← go:embed + LoadAll / TemplatesFor / FormsAndPolicies
├── materialiser.go     ← Materialiser + Report + clinic-create hook
├── loader_test.go      ← unit tests
└── data/               ← embedded YAML library (100 templates)
    ├── _terms.md       ← user-facing disclaimer (rendered in app + PDF)
    ├── _schema.md      ← form/policy YAML format specification
    ├── INDEX.md        ← auto-generated catalogue
    ├── shared/         ← cross-cutting (3 forms + 8 policies)
    ├── veterinary/     ← 6 forms + 6 policies
    ├── dental/         ← 9 forms + 7 policies
    ├── general_clinic/ ← 10 forms + 10 policies
    └── aged_care/      ← 17 forms + 24 policies
```

The YAML tree lives inside `sal/` because Go's `embed` cannot follow
symlinks outside the module. The source of truth is the embedded copy;
there is no second copy at the repo root.

---

## Counts

| Folder | Forms | Policies | Total |
|---|---:|---:|---:|
| `shared/` | 3 | 8 | 11 |
| `veterinary/` | 6 | 6 | 12 |
| `dental/` | 9 | 7 | 16 |
| `general_clinic/` | 10 | 10 | 20 |
| `aged_care/` | 17 | 24 | 41 |
| **Total** | **45** | **55** | **100** |

A clinic with `vertical = dental` and `country = NZ` installs everything in
`shared/` plus everything in `dental/` — 27 forms + policies, country-keyed
clauses rendered for NZ only.

---

## Lineage migration

`migrations/00091_salvia_provided_content_lineage.sql` adds four columns to
both `forms` and `policies`:

| Column | Type | Purpose |
|---|---|---|
| `salvia_template_id` | `TEXT` | Stable id from the YAML (`salvia.shared.consultation_note`). NULL = clinic-authored. |
| `salvia_template_version` | `INT` | YAML version. Bumps when Salvia ships a revision. Drives upgrade UX. |
| `salvia_template_state` | `VARCHAR(16)` | `default` / `forked` / `deleted`. `forked` is set on first edit by the forms / policy service. |
| `framework_currency_date` | `DATE` | The regulator framework's last-reviewed date. Renders as a chip in the Library panel and in PDF footers. |

Mutually exclusive with the marketplace lineage columns (`source_marketplace_*`)
from migration 00088 — a row carries one provenance or the other, not both.

### Idempotency

`UNIQUE INDEX (clinic_id, salvia_template_id) WHERE salvia_template_id IS NOT NULL`
on both tables. The materialiser is safe to re-run; the second insert hits the
index and is logged as a per-template error, not a fatal abort.

---

## Loading

`LoadAll()` walks `data/` at startup. Every `*.yaml` file is parsed into a
`Template` struct and validated against the rules in `data/_schema.md`. A
malformed file fails the boot — silently shipping a broken catalogue is the
wrong failure mode for a compliance feature, exactly like the drugs catalog.

Validation enforces:

1. `id` is non-empty, prefixed `salvia.`, globally unique.
2. `vertical` is one of `shared / veterinary / dental / general_clinic / aged_care` **and** matches the parent folder name.
3. `countries` is a non-empty subset of `[NZ, AU, UK, US]`.
4. `currency_date` parses as `YYYY-MM-DD` and is non-zero.
5. Forms have ≥1 field; every field has a non-empty `key` + `type`.
6. Policies have ≥1 clause; every clause has a non-empty `id` + `title`.
7. `body_per_country` keys are a subset of the file's declared `countries`.

The `Badge`, `Disclaimer`, and `Maintainer` fields default-inject from package
constants if the YAML omits them — files MAY include them for clarity but
shouldn't drift.

---

## Materialising into a clinic

```go
// internal/salvia_content/materialiser.go
type Materialiser struct {
    loaded   []Template
    forms    FormsService
    policies PolicyService
    logger   *slog.Logger
}

func NewMaterialiser(forms FormsService, policies PolicyService, log *slog.Logger) (*Materialiser, error)

func (m *Materialiser) MaterialiseFor(
    ctx context.Context,
    clinicID uuid.UUID,
    vertical domain.Vertical,
    country string,
    staffID uuid.UUID,
) Report
```

`FormsService` and `PolicyService` are narrow interfaces declared in the
package — they are slices of `forms.Service` and `policy.Service` exposing
only the `CreateForm` / `CreatePolicy` methods. The materialiser never
imports the full forms or policy package types beyond the input structs
it needs.

For each applicable template:

- Calls `forms.CreateForm` (for `KindForm`) or `policy.CreatePolicy` (for
  `KindPolicy`) with the `Salvia*` lineage fields populated, plus
  `state = "default"` and the parsed `framework_currency_date`.
- Per-template failures are logged and the loop continues. The materialiser
  returns a `Report` summarising what installed and what failed.

The package never writes to `forms.forms` or `policies.policies` directly —
all writes go through the existing service layer.

### V1 scope — light rows

The materialiser installs `forms` and `policies` rows with the right name,
description, and lineage. **It does not populate draft fields / clauses from
the YAML at install time.** That is by design for V1:

- Clinic-create stays fast (a vet clinic gets ~12 + 11 = 23 rows; aged
  care 41 + 11 = 52). All inserts complete inside the compliance-submit
  request.
- The draft remains empty until either a clinician publishes (V2 feature —
  copy YAML content into a real version) or forks the template (state =
  `forked`, content copied at fork time).
- Render-time enrichment: when the Library panel or the form editor opens a
  row in `default` state, the YAML body can be pulled live from the embed
  via `salvia_template_id`. (Wire-up of this read path is V1.5; for V1 the
  Library panel surfaces the row's metadata only.)

---

## Wiring into clinic-create

The hook fires from `clinic.Service.SubmitCompliance`:

```go
// internal/clinic/service.go
if s.salviaMaterialiser != nil && row.Country != "" && row.Vertical != "" {
    s.salviaMaterialiser.MaterialiseFor(ctx, clinicID, row.Vertical, row.Country, in.StaffID)
}
```

By the time `SubmitCompliance` runs, all three inputs are available:

| Input | Set at | Stored on |
|---|---|---|
| Vertical | `clinic.Register` (clinic-create) | `clinics.vertical` |
| Country | `clinic.Update` (onboarding wizard, country picker step) | `clinics.country` |
| StaffID | The auth context of the compliance submitter | passed via `SubmitComplianceInput.StaffID` |

If country is empty (handoff path, dev seeds) the materialiser is skipped
silently — clinics that complete compliance without selecting a country
will need the FE to ensure country is always set first.

The cross-package wiring is the `clinic.SalviaContentMaterialiser` interface
in the clinic package + the `clinicSalviaContentAdapter` in `app.go`:

```go
// internal/app/app.go
salviaContentMat, err := salvia_content.NewMaterialiser(formsSvc, policySvc, log)
if err != nil { return nil, fmt.Errorf("app.Build: salvia_content materialiser: %w", err) }
clinicSvc.SetSalviaContentMaterialiser(&clinicSalviaContentAdapter{m: salviaContentMat})
```

The clinic package never imports `salvia_content` (dependency runs the
other way), preserving the cross-domain rule from `CLAUDE.md`.

---

## API surface

`FormResponse` and `PolicyResponse` carry the lineage fields:

```json
{
  "id": "...",
  "name": "Consultation note",
  "salvia_template_id": "salvia.shared.consultation_note",
  "salvia_template_version": 1,
  "salvia_template_state": "default",
  "framework_currency_date": "2026-05-08"
}
```

The Flutter app filters on `salvia_template_id != null` to build the Library
panel; the badge surface is the same on every list (forms list, policies
list, form editor header).

---

## Lifecycle (per clinic, per template)

```
                    ┌──────────┐
   materialiser ──► │ default  │ ◄── clinic on unmodified Salvia content;
                    └────┬─────┘     receives upgrade banners when v2 ships
                         │
        clinician edits  │
                         ▼
                    ┌──────────┐
                    │  forked  │     clinic owns content; lineage retained
                    └────┬─────┘     for audit; Salvia does not push updates
                         │
       clinician deletes │
                         ▼
                    ┌──────────┐
                    │ deleted  │     row stays; re-sync never recreates;
                    └──────────┘     clinic can re-add from Library panel
                                     to get a fresh `default` copy
```

The `state` column is in place from migration 00091; the explicit fork /
delete / re-add actions in the Library panel are V1.5 deliverables. V1
ships only the read view + the default state set by the materialiser.

---

## Adding or updating a template

1. Edit / add the YAML under `data/<vertical>/<forms|policies>/<slug>.yaml`.
   Follow the spec in `data/_schema.md`.
2. Bump `version:` on the file (start at 1 for new files).
3. Update `currency_date:` to today's date if the change reflects a
   regulator-framework refresh; leave it as-is for typo / wording fixes.
4. Run `go test ./internal/salvia_content/...` — the loader test will fail
   the build on any structural issue.
5. Update `data/INDEX.md` (currently hand-maintained — TODO: add a
   `go generate` step that emits it from `LoadAll()`).
6. Submit for clinical-compliance review before merging if the change is
   material — that review is the launch gate from `_terms.md`, not
   bypassable.

---

## Currency commitments

These are operational, not enforced in code:

- **Routine review** every 6 months per market.
- **Emergency revision** within 30 days of a major regulator publication
  (e.g. NZ DCNZ Sedation Practice Standard March 2026, UK CQC registration
  changes 9 Feb 2026, AU SIRS guidance reissue Feb 2026, US HIPAA NPP
  update 16 Feb 2026, US MDS 3.0 v1.20.1 1 Oct 2025).
- **Public-facing** `framework_currency_date` is rendered on every Library
  row in-app and in PDF footers via the doc-theme system.

The loader emits a warning (not error) when a file's `currency_date` is
older than 12 months — visible at `make dev` startup. Beyond that the
clinic remains responsible per the disclaimer.

---

## Operational notes

- **Boot fails** on malformed YAML — by design. A bad regulator template is
  worse than no template; the clinic shouldn't quietly start drifting.
- **Best-effort install.** The materialiser logs and continues on per-row
  errors; clinic compliance submission never blocks on a failed install.
- **Idempotent re-sync.** The unique index makes re-running safe. Today's
  trigger is `SubmitCompliance`; future deliverables may add an explicit
  admin-only `/api/v1/me/salvia-content/resync` endpoint for fix-ups.
- **No background jobs.** Materialisation is synchronous within the
  compliance request. ~52 INSERTs × ~10 ms = ~500 ms upper bound for
  aged care; acceptable for a once-per-clinic event.
- **No removal.** Salvia never deletes a clinic's templates unilaterally.
  The clinic owns the rows once installed.

---

## Cross-references

- `SALVIA_PROVIDED_CONTENT.md` (repo root) — product spec, distribution model,
  legal framing, country deltas.
- `internal/salvia_content/data/_terms.md` — user-facing Terms of Use.
- `internal/salvia_content/data/_schema.md` — YAML format spec.
- `MARKETPLACE_BACKLOG.md` (repo root) — why the marketplace is shelved.
- `docs/forms.md` — the forms module itself (Salvia content uses it
  underneath).
- `docs/policy.md` — the policy module itself.
- Flutter Library panel — `salvia/apps/lib/features/settings/ui/settings_salvia_library_page.dart`.
- In-app help topic — `salvia_library` (Settings & ops → Salvia Library).
