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
                │  Published   │──── rollback ──► new Draft
                └──────┬───────┘
                       │ (optional) retire
                       ▼
                ┌──────────────┐
                │   Retired    │  archived_at set, reason recorded
                └──────────────┘
```

### One living draft

Only one draft per form is allowed (enforced by a `UNIQUE(form_id) WHERE status = 'draft'` partial index). Publishing consumes the draft. A new draft is created automatically on the next edit — or explicitly via rollback.

### Semver versioning

| Event | Before | After |
|---|---|---|
| First publish (any) | — | **1.0** |
| `change_type=major` (field structure changed) | 1.2 | **2.0** |
| `change_type=minor` (metadata/prompts only) | 1.2 | **1.3** |

### Rollback

Creates a new draft by copying all fields from a previously published version. Any existing draft must be discarded first. Publish the resulting draft (as a major version) to make the rollback live. The draft carries a `rollback_of` pointer to the source version for audit purposes.

### Retire / decommission

Sets `archived_at` on the form and records an optional `retire_reason`. In-flight notes (audio currently processing with this form) complete normally. Forms are hidden from the default list but visible with `include_archived=true`.

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

Adding a new type never requires a migration — just update the Flutter builder.

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

---

## PDF style settings

Each clinic has a single set of PDF export settings, versioned independently:

- **Logo** — object-storage key; Flutter fetches and renders it
- **Primary colour** — hex string e.g. `#3B82F6`
- **Font family** — name recognised by the Flutter PDF renderer
- **Header extra** — additional text below clinic name/logo
- **Footer text** — custom text; form version and approver appended automatically

Every `PUT /api/v1/clinic/form-style` creates a new immutable version row (version N+1). The latest version is always used for PDF generation. Previous versions are retained for audit.

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
| `POST` | `/api/v1/forms/{form_id}/publish` | Publish draft; assigns semver |
| `POST` | `/api/v1/forms/{form_id}/policy-check` | Run policy compliance check on draft |
| `POST` | `/api/v1/forms/{form_id}/rollback` | Create draft from prior published version |
| `POST` | `/api/v1/forms/{form_id}/retire` | Retire/decommission form |
| `GET` | `/api/v1/forms/{form_id}/versions` | Published version history (newest first) |

### Policy links

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/forms/{form_id}/policies` | List linked policy IDs |
| `POST` | `/api/v1/forms/{form_id}/policies` | Link a policy |
| `DELETE` | `/api/v1/forms/{form_id}/policies/{policy_id}` | Unlink a policy |

### PDF style

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/clinic/form-style` | Get current style (null body if none set) |
| `PUT` | `/api/v1/clinic/form-style` | Update style (creates new version) |

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

Migration: `00009_create_forms.sql`

### Tags

`tags TEXT[]` on `forms` with a GIN index. Free-form strings set by the admin. Use `?tag=cardiology` to filter forms.

---

## Testing

Unit tests: `internal/forms/service_test.go` — 18 tests using `fakeRepo`, no database.

```bash
make test                       # all unit tests
go test ./internal/forms/... -v # forms only
```

Integration tests (database required) are under `//go:build integration` and follow the same `testutil.NewTestDB` pattern as other modules.
