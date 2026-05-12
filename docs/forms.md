# Forms

Forms are the template engine of Salvia. Clinic administrators build structured form templates in a drag-and-drop builder; staff record audio and the AI extraction pipeline fills in the form from the transcript. The filled form becomes the clinical note.

---

## Key concepts

| Concept | What it is |
|---|---|
| **Form** | A named template owned by a clinic, optionally grouped in a folder |
| **Group** | A single-level folder for organising forms (no nesting) |
| **Draft** | The single mutable version of a form being edited; exactly one per form at any time |
| **Published version** | A frozen, immutable snapshot; assigned a semver number when published |
| **Field** | A typed input on a version; type is an open string so new field types never need schema migrations |
| **Policy link** | A many-to-many link to a compliance policy; FK to the `policies` table enforced at the database level |
| **Style version** | The clinic's PDF export style settings (logo, colour, font, header/footer); versioned independently from forms |

---

## Form lifecycle

```
                ┌──────────────┐
     create ──► │    Draft     │ ◄── update fields / metadata
                └──────┬───────┘
                       │ publish (assigns vX.Y)
                       ▼
                ┌──────────────┐
                │  Published   │──── rollback ──► new Published
                └──────┬───────┘        (copies fields from target,
                       │                 bumps minor, records
                       │                 rollback_of)
                       │ (optional) retire
                       ▼
                ┌──────────────┐
                │   Retired    │  archived_at set, reason recorded
                └──────────────┘
```

### One living draft

Only one draft per form is allowed (enforced by a `UNIQUE(form_id) WHERE status = 'draft'` partial index, migration 00009). Publishing consumes the draft. A new draft is created automatically on the next edit.

### Semver versioning

| Event | Before | After |
|---|---|---|
| First publish (any) | — | **1.0** |
| `change_type=major` (field structure changed) | 1.2 | **2.0** |
| `change_type=minor` (metadata/prompts only) | 1.2 | **1.3** |
| Rollback to any prior version | 2.0 | **2.1** |

Version numbers (`form_id`, `version_major`, `version_minor`) are protected by a partial unique index on `status = 'published'` (migration 00036). A concurrent publish + rollback racing on the same pair fails fast rather than landing two "v2.1" rows; the service retries once with a recomputed number.

### Publish preconditions

- Form must not be retired (`archived_at IS NULL`).
- Draft must exist (a fresh form has one; re-publishing after publish requires an `UpdateDraft` first).
- **Draft must have ≥ 1 field.** Publishing an empty draft is rejected with `ErrConflict`: the extraction pipeline would have nothing to populate and the PDF renderer nothing to render, and the version history would carry a useless row.

### Rollback

Publishes a **new immutable version** whose fields are copied from the target published version. The new row carries `rollback_of = <target_version_id>` so the history shows provenance. Rollback does **not** touch the existing draft — any WIP the user has open survives untouched, because rolling back is a corrective action against the published history, not an editing operation.

Semver: rollback bumps the **minor** over the current latest (a rollback is "a small course correction", not a breaking re-architecture). So if the latest published is 2.0 and the user rolls back to 1.0, the new live version is 2.1 with the fields of 1.0.

The repository writes the new version row and all its field rows in a single transaction (`CreatePublishedVersionWithFields`). A partial failure can't leave a zero-field published version in the history. A collision on the semver pair (two concurrent rollbacks, or a rollback racing a publish) returns `ErrConflict`; the service retries once with a recomputed number.

### Retire / decommission

Sets `archived_at` on the form and records an optional `retire_reason`. In-flight notes (audio currently processing with this form) complete normally. Forms are hidden from the default list but visible with `include_archived=true`.

---

## Provenance — where a form came from

A form is created in one of three ways:

| Origin | Lineage columns | Migration | Notes |
|---|---|---|---|
| **Clinic-authored** | all NULL | — | Default. Created via the in-app form builder. |
| **Imported from marketplace** | `source_marketplace_listing_id` / `_version_id` / `_acquisition_id` set | 00088 | Created by the marketplace import service. Powers the `marketplace_lineage_banner` and sibling-form lookup. Marketplace itself is currently shelved (see `marketplace.md`). |
| **Installed by Salvia** | `salvia_template_id` / `_version` / `_state` / `framework_currency_date` set | 00091 | Created by the `salvia_content` materialiser at clinic onboarding. Powers the "Made by Salvia v1" badge and the Settings → Salvia Library panel. See `salvia-content.md`. |

The two provenance kinds are mutually exclusive — a form carries marketplace OR Salvia lineage, not both. `forms.repository.scanForm` reads all eight columns; `FormResponse` exposes them via JSON.

### Salvia template lifecycle

For Salvia-installed forms, `salvia_template_state` tracks per-clinic lifecycle:

- `default` — clinic on unmodified Salvia content; receives upgrade banners when v2 ships.
- `forked` — clinician edited the form. Badge clears; clinic owns the content; lineage retained for audit.
- `deleted` — clinician explicitly removed. Row stays; the materialiser will not re-create. Clinic can re-add from the Library panel to get a fresh `default` copy.

The unique index `idx_forms_salvia_template_per_clinic (clinic_id, salvia_template_id)` enforces idempotency — re-running the materialiser cannot double-install.

---

## Field types

`type` is a free-form string — the Flutter builder and renderer interpret it. The backend stores whatever the client sends. `config` is type-specific JSONB. Common examples:

| type | config example |
|---|---|
| `text` | `{}` |
| `long_text` | `{"max_length": 2000}` |
| `number` | `{"unit": "kg"}` |
| `decimal` | `{"precision": 1}` |
| `slider` | `{"min": 0, "max": 10, "step": 1}` |
| `select` | `{"options": ["mild","moderate","severe"]}` |
| `button_group` | `{"options": ["yes","no","unknown"]}` |
| `percentage` | `{}` |
| `blocks` | `{"count": 10, "labels": ["none","max"]}` |
| `image` | `{"allow_annotation": true}` |
| `date` | `{}` |
| `system.consent` | `{"consent_type": "treatment_plan", "require_witness": false}` |
| `system.drug_op` | `{"operation": "administer", "controlled_only": false, "confirm_required": true}` |
| `system.incident` | `{"incident_type": "fall", "min_severity": "low"}` |
| `system.pain_score` | `{"scale_id": "flacc"}` |

Adding a new type never requires a migration — just update the Flutter builder.

### System widgets

The `system.*` types are **typed compliance fields** — they don't store
free text, they capture into the `consent_records` /
`drug_operations_log` / `incident_events` / `pain_scores` ledger tables
and store the resulting entity id back into `note_fields.value` as an
id-pointer (e.g. `{"pain_score_id":"<uuid>"}`).

The flow:

1. AI extracts a typed JSON suggestion into `note_fields.value` (or
   leaves it empty if it didn't hear one — clinician captures
   manually).
2. Clinician reviews / edits the suggestion in the rich capture panel
   on the note review surface.
3. Submit lands on a per-type materialise endpoint which creates the
   ledger row + writes the id-pointer back. See `notes.md` →
   "System widgets".

Per-type config schemas live in `internal/forms/schema/registry.go`:

- `SystemConsentConfig` — pin a `consent_type`; `require_witness`
  forces a witness regardless of capture mode.
- `SystemDrugOpConfig` — pin an `operation`; `controlled_only` filters
  the shelf picker to controlled drugs; `confirm_required` (default
  true) keeps the row in `pending_confirm` until the clinician taps
  Confirm. **Cannot be disabled for `administer` / `dispense`** — the
  validator rejects that as a regulator-binding override.
- `SystemIncidentConfig` — pin an `incident_type`; `min_severity` floors
  AI's extracted severity so an incident-specific form (e.g.
  fall-only) always lands at ≥ that level.
- `SystemPainScoreConfig` — pin a `scale_id` (`nrs`/`flacc`/`painad`/
  `wong_baker`/`vrs`/`vas`).

`schema.IsSystemFieldType` is the canonical check; `ValidateConfig`
enforces the per-type schemas at form publish time.

### Field flags

| Flag | Meaning |
|---|---|
| `required` | AI extraction must provide a value, or the note is flagged incomplete |
| `skippable` | Field excluded from AI extraction entirely; reviewer fills it manually |

Both flags are independent. A `required` field should not be `skippable` (enforced by the Flutter builder; backend stores whatever is sent).

---

## Policy compliance check

Before publishing, staff can run **Check policy alignment**:

1. Click the button in the form builder
2. `POST /api/v1/forms/{form_id}/policy-check`
3. The result is stored on the draft version (`policy_check_result`, `policy_check_at`)
4. Staff review issues and fix fields before publishing

> **Note:** Full LLM-based scoring against clause parity levels is tracked in the [backlog](backlog.md#pending-policy-alignment-score-on-notes). Currently returns a placeholder message. The policy engine is built — the scorer implementation is what remains.

The form must have at least one policy linked before the check can run.

### Cross-tenant protection on policy links

`POST /api/v1/forms/{form_id}/policies` verifies that the supplied `policy_id` belongs to the caller's clinic before creating the link. Without this check, a tenant could enumerate policy UUIDs and attach another clinic's policy to their own form; the subsequent `policy-check` response would then leak that clinic's clause text. The verifier is wired in `app.go` (`formsPolicyOwnershipAdapter`) and returns `ErrNotFound` for both missing and foreign policies so the failure modes are indistinguishable from the outside.

---

## PDF style settings

Each clinic has a single set of PDF export settings, versioned independently:

- **Logo** — object-storage key; Flutter fetches and renders it
- **Primary colour** — hex string e.g. `#3B82F6`
- **Font family** — name recognised by the Flutter PDF renderer
- **Header extra** — additional text below clinic name/logo
- **Footer text** — custom text; form version and approver appended automatically

Every `PUT /api/v1/clinic/form-style` creates a new immutable version row (version N+1). The latest version is always used for PDF generation. Previous versions are retained for audit.

### Logo upload validation

`POST /api/v1/clinic/form-style/logo` (multipart, field `file`, max 4 MiB) validates uploads in two phases:

1. The client-declared `Content-Type` header is checked against the allowlist: `image/png`, `image/jpeg` (`image/jpg` is accepted as an alias), `image/webp`. SVG is rejected — scriptable XML doesn't belong in a doc-theme logo when the signed-URL flow serves it back to browsers.
2. The file's first bytes are sniffed for a magic signature and must match the declared type. This defeats a caller who uploads HTML or executables under a forged `Content-Type: image/png`, which would otherwise turn a later signed URL into an XSS vector.

Mismatch returns `415 Unsupported Media Type`. The canonicalised MIME string (with aliases collapsed) is what gets persisted, so storage and the signed URL are always consistent.

---

## Endpoints

All endpoints require `ManageForms` permission.

### Form groups

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/form-groups` | Create a group |
| `GET` | `/api/v1/form-groups` | List all groups (ordered alphabetically) |
| `PATCH` | `/api/v1/form-groups/{group_id}` | Update group name/description |

### Forms

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/forms` | Create form (empty draft created automatically) |
| `GET` | `/api/v1/forms` | List forms (pagination, group/tag filters) |
| `GET` | `/api/v1/forms/{form_id}` | Get form with draft + latest published + fields |
| `PUT` | `/api/v1/forms/{form_id}/draft` | Replace draft fields and metadata (full replacement) |
| `POST` | `/api/v1/forms/{form_id}/publish` | Publish draft; assigns semver (rejects empty drafts with 409) |
| `POST` | `/api/v1/forms/{form_id}/policy-check` | Run policy compliance check on draft |
| `POST` | `/api/v1/forms/{form_id}/rollback` | Publish a new version with fields copied from a prior published version |
| `POST` | `/api/v1/forms/{form_id}/retire` | Retire/decommission form |
| `GET` | `/api/v1/forms/{form_id}/versions` | Published version history (newest first) |

### Policy links

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/forms/{form_id}/policies` | List linked policy IDs (response shape: `{"policy_ids": ["..."]}`) |
| `POST` | `/api/v1/forms/{form_id}/policies` | Link a policy (verifies policy belongs to caller's clinic) |
| `DELETE` | `/api/v1/forms/{form_id}/policies/{policy_id}` | Unlink a policy |

### PDF style

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/clinic/form-style` | Get current style (null body if none set) |
| `PUT` | `/api/v1/clinic/form-style` | Update style (creates new version) |
| `POST` | `/api/v1/clinic/form-style/logo` | Upload a doc-theme logo (multipart; ≤ 4 MiB; png/jpeg/webp with matching magic bytes) |

---

## Database tables

```
form_groups              — clinic folders
forms                    — form templates (archived_at = retired)
form_versions            — immutable published snapshots + single draft
form_fields              — fields on a specific version
form_policies            — many-to-many form↔policy pointers
clinic_form_style_versions — versioned PDF style settings per clinic
```

Migrations:
- `00009_create_forms.sql` — initial schema, including `UNIQUE(form_id) WHERE status = 'draft'`
- `00036_form_version_unique_published_semver.sql` — partial unique index on `(form_id, version_major, version_minor) WHERE status = 'published'` to prevent a publish + rollback race from producing two rows with the same semver

### Tags

`tags TEXT[]` on `forms` with a GIN index. Free-form strings set by the admin. Use `?tag=cardiology` to filter forms.

---

## Testing

Unit tests: `internal/forms/service_test.go` uses an in-memory `fakeRepo` (no database). Notable cases:

- `TestService_PublishForm_*` — semver bump rules, empty-draft rejection, partial-unique retry
- `TestService_RollbackForm_CopiesFieldsFromTarget` — rollback produces a new published version with `rollback_of` set and the target's fields copied
- `TestService_RollbackForm_PreservesExistingDraft` — rolling back never destroys WIP
- `TestService_LinkPolicy_RejectsCrossTenantPolicy` — `PolicyOwnershipVerifier` rejects foreign policy UUIDs
- `TestSniffImageType` / `TestCanonicalStyleLogoType` — logo upload magic-byte + MIME allowlist

```bash
make test                       # all unit tests
go test ./internal/forms/... -v # forms only
```

Integration tests (database required) are under `//go:build integration` and follow the same `testutil.NewTestDB` pattern as other modules.
