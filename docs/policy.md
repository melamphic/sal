# Policy Engine

The policy engine lets clinics manage their internal compliance policies — NABH, NZ Best Practice, VMR, AVMA, and any custom frameworks. Policies are stored as block-based content (AppFlowy-compatible JSON), versioned with the same semver scheme as forms, and linked to forms to enable compliance checking.

---

## Key concepts

| Concept | What it is |
|---|---|
| **Policy** | A named policy document owned by a clinic, optionally in a folder |
| **Folder** | A single-level grouping for organising policies (no nesting) |
| **Draft** | The single mutable version being edited; exactly one per policy |
| **Published version** | A frozen snapshot with a semver number |
| **Content** | JSONB array of AppFlowy-compatible editor blocks; opaque to the backend |
| **Clause** | A specific block marked as enforceable, with a parity level |
| **Parity** | Enforcement level: `high` = must follow, `medium` = should follow, `low` = try to follow |

---

## Policy lifecycle

```
                ┌──────────────┐
     create ──► │    Draft     │ ◄── update content / metadata
                └──────┬───────┘
                       │ publish (assigns vX.Y)
                       ▼
                ┌──────────────┐
                │  Published   │──── rollback ──► new Draft
                └──────┬───────┘
                       │ retire
                       ▼
                ┌──────────────┐
                │   Retired    │  archived_at set, all form links removed
                └──────────────┘
```

### One draft at a time

Exactly one draft per policy is enforced by a `UNIQUE(policy_id) WHERE status = 'draft'` partial index. Publishing consumes the draft. The next edit creates a new draft automatically.

### Semver versioning

| Event | Before | After |
|---|---|---|
| First publish | — | **1.0** |
| `change_type=major` | 1.2 | **2.0** |
| `change_type=minor` | 1.2 | **1.3** |

### Rollback

Creates a new draft with the content copied from a prior published version. The draft carries a `rollback_of` pointer for audit purposes. Requires discarding any existing draft first.

### Retire

Archives the policy (`archived_at` + optional `retire_reason`) and **automatically removes all form links** pointing to the retired policy. The form's `form_policies` rows are deleted; the forms themselves are unaffected. Forms linked to the retired policy will show the link as removed on next load.

---

## Provenance — where a policy came from

A policy is created in one of three ways:

| Origin | Lineage columns | Migration | Notes |
|---|---|---|---|
| **Clinic-authored** | all NULL | — | Default. Created via the in-app policy editor. |
| **Imported from marketplace** | `source_marketplace_version_id` set | 00033 | Created by the marketplace import service. Marketplace UI is currently shelved (see `marketplace.md`). |
| **Installed by Salvia** | `salvia_template_id` / `_version` / `_state` / `framework_currency_date` set | 00091 | Created by the `salvia_content` materialiser at clinic onboarding. Powers the "Made by Salvia v1" badge and the Settings → Salvia Library panel. See `salvia-content.md`. |

The two provenance kinds are mutually exclusive — a policy carries marketplace OR Salvia lineage, not both. `policy.repository.scanPolicy` reads all five lineage columns; `PolicyResponse` exposes them via JSON. The Salvia state lifecycle (`default` / `forked` / `deleted`) is identical to the forms side — see `salvia-content.md` for the full description.

The unique index `idx_policies_salvia_template_per_clinic (clinic_id, salvia_template_id)` enforces materialiser idempotency.

---

## Block-based content

Policy content is stored as a JSONB array of editor blocks. The backend treats this as entirely opaque — no parsing, no validation of block structure. The Flutter client (using the AppFlowy OSS package) handles rendering and editing. This means new block types (headings, callouts, tables, checklists) are supported without any backend changes.

Example content shape:
```json
[
  {"type": "heading1", "delta": [{"insert": "Medication Administration Policy"}]},
  {"type": "paragraph", "delta": [{"insert": "All controlled substances must be..."}]},
  {"type": "bulleted_list", "delta": [{"insert": "Verify dosage against patient weight"}]}
]
```

---

## Clauses

Blocks can be marked as **enforceable clauses** to drive the policy alignment score on notes. Each clause:

- References a `block_id` (client-assigned; stable across edits)
- Has a human-readable `title`
- Carries a `parity` level: `high`, `medium`, or `low`

Clauses are stored separately from content so they can be queried efficiently without parsing the full JSONB. Clauses are replaced atomically (DELETE + INSERT in a transaction) via the PUT endpoint.

---

## Form linking

Forms link to policies via `form_policies` (many-to-many). The FK `form_policies.policy_id → policies.id` is now enforced at the database level (added in migration `00014`).

Linking/unlinking is managed from the **forms** side:
- `POST /api/v1/forms/{form_id}/policies` — link a policy
- `DELETE /api/v1/forms/{form_id}/policies/{policy_id}` — unlink a policy

When a policy is retired, the policy engine calls the `FormLinker` interface (wired in `app.go`) to remove all links automatically.

---

## PDF/Doc import (planned)

Clinics with existing policies can upload PDFs or Word documents. A River job will extract the text, call AI to structure it into blocks and identify enforceable clauses, and create a new policy draft with the result. See [Backlog](backlog.md) for implementation details.

---

## Endpoints

All endpoints require `manage_policies` permission. Rollback requires `rollback_policies`.

### Folders

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/policy-folders` | Create a folder |
| `GET` | `/api/v1/policy-folders` | List all folders |
| `PUT` | `/api/v1/policy-folders/{folder_id}` | Rename a folder |

### Policies

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/policies` | Create policy (empty draft created automatically) |
| `GET` | `/api/v1/policies` | List policies (pagination, folder filter, include_archived) |
| `GET` | `/api/v1/policies/{policy_id}` | Get policy with draft + latest published |
| `PUT` | `/api/v1/policies/{policy_id}/draft` | Update draft content and metadata |
| `POST` | `/api/v1/policies/{policy_id}/publish` | Publish draft; assigns semver |
| `POST` | `/api/v1/policies/{policy_id}/rollback` | Create draft from prior published version |
| `POST` | `/api/v1/policies/{policy_id}/retire` | Archive policy and remove all form links |

### Versions

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/policies/{policy_id}/versions` | Published version history (newest first) |
| `GET` | `/api/v1/policies/{policy_id}/versions/{version_id}` | Get a specific version with full content |

### Clauses

| Method | Path | Description |
|---|---|---|
| `PUT` | `/api/v1/policies/{policy_id}/versions/{version_id}/clauses` | Replace all clauses (atomic) |
| `GET` | `/api/v1/policies/{policy_id}/versions/{version_id}/clauses` | List clauses for a version |

---

## Database

Migration: `00014_create_policies.sql`

```
policy_folders      — single-level folders
policies            — policy metadata (archived_at = retired)
policy_versions     — immutable published snapshots + single mutable draft; content as JSONB
policy_clauses      — enforceable clause markers referencing block_ids
```

`policy_versions` has a `UNIQUE(policy_id) WHERE status = 'draft'` partial index enforcing one draft per policy, mirroring the pattern used for `form_versions`.
