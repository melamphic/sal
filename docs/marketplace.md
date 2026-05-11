# Marketplace — Architecture Reference

!!! warning "Status: shelved 2026-05-08"
    The marketplace UI is currently gated behind `kMarketplaceEnabled = false`
    at `salvia/apps/lib/core/feature_flags.dart`. The activity-bar entry,
    AppBar action, watchcard, and all six `/marketplace/*` routes are
    hidden / redirect to home. Backend routes (`/api/v1/marketplace/*`),
    permissions, migrations, and tables remain in place — flipping the flag
    restores the UI without redeployment.
    
    Marketplace was paused mid-flight to refocus on **Salvia-provided
    prebuilt content** (forms + policies seeded into every clinic at
    signup; see `salvia-content.md`). Restart triggers are documented in
    `MARKETPLACE_BACKLOG.md` at the repo root.
    
    The architecture reference below describes the shipped state.

Status: **Phase 1 + 1.5 + 2 + 3 implemented.** Migrations `00019`, `00020`, plus marketplace-track lineage in `00088`–`00090`, land the schema; `internal/marketplace/` carries repository + service + handlers + Stripe client.

Marketplace lives entirely **behind the post-login UI**. No public / unauthenticated marketplace routes. Stripe webhook is the only endpoint without JWT, verified by signature.

---

## 1. Actor Model

Three publisher kinds — captured via `publisher_accounts.authority_type`:

| Kind | `authority_type` | Can publish | Can grant badges | Can suspend listings | Platform fee |
|---|---|---|---|---|---|
| **Regular clinic** | `NULL` (or absent publisher_account) | Own listings | — | — | 30% |
| **Authority body** (NZVA-like) | `'authority'` | Own listings | `verified_badge` within own vertical only | — | 0% |
| **Salvia** | `'salvia'` | Any | Anything (including authority grants) | Any | 0% |

Key consequences:
- Any clinic can self-register as a publisher — no approval queue.
- Only Salvia grants `authority_type` on target publishers.
- An authority body can grant `verified_badge` but NOT grant authority.
- Badge grantors can revoke their own grants (FK `authority_granted_by`). Salvia can revoke anything.

The Salvia platform clinic is bootstrapped by migration `00020` — a single reserved clinic row + publisher_account with `authority_type = 'salvia'`. Salvia employees are staff of that clinic.

---

## 2. Client Lifecycle

### 2.1 Regular clinic becoming a publisher

```
POST /api/v1/marketplace/publishers                (perm_marketplace_manage)
  └─ status = 'active' immediately (no queue)
  └─ gated on clinic.status = 'active' (trial → 403)

POST /api/v1/marketplace/publishers/{id}/stripe-onboarding
  └─ creates Stripe Connect Express account (Express, not Custom)
  └─ returns hosted onboarding URL
  └─ account.updated webhook flips stripe_onboarding_complete = true
```

### 2.2 Publishing a listing + version

```
POST /api/v1/marketplace/listings                  (perm_marketplace_manage)
  body:
    publisher_account_id, vertical, name, slug, short_description,
    bundle_type = 'bundled' | 'form_only'   (default 'bundled')
    pricing_type = 'free' | 'paid'
    price_cents (required iff paid)
  └─ caller's clinic must own the publisher
  └─ created as status='draft'

POST /api/v1/marketplace/listings/{id}/versions    (perm_marketplace_manage)
  body: source_form_id, change_type, change_summary
  ├─ snapshots source form (via FormSnapshotter interface)
  ├─ if bundle_type='bundled' AND form has linked policies:
  │    for each policy → SnapshotPolicy → embed content + clauses in package
  ├─ computes SHA-256 checksum of canonical payload
  ├─ writes marketplace_versions + marketplace_version_fields atomically
  └─ enqueues upgrade notifications for existing active acquisitions

POST /api/v1/marketplace/listings/{id}/publish     (perm_marketplace_manage)
  └─ draft|under_review → published
  └─ requires at least one version
```

### 2.3 Consumer browse + acquire

```
GET /api/v1/marketplace/listings                   (authenticated)
  └─ BrowseListings auto-scopes to caller's clinic.vertical
  └─ Salvia clinic can browse cross-vertical (AuthType = 'salvia')

GET /api/v1/marketplace/listings/{slug}
GET /api/v1/marketplace/listings/{id}/versions/{version_id}
  └─ paid listings expose only first preview_field_count fields; rest Locked=true

POST /api/v1/marketplace/listings/{id}/acquire     (perm_marketplace_download)
  └─ free listings only; paid listings → 409 (use purchase)
  └─ trial clinics can acquire free

POST /api/v1/marketplace/listings/{id}/purchase    (perm_marketplace_download)
  └─ creates pending acquisition + Stripe PaymentIntent with:
       application_fee_amount = price × fee_pct
       where fee_pct = 0 for salvia/authority, 30 otherwise
       transfer_data.destination = publisher.stripe_connect_account_id
  └─ returns client_secret; client confirms via Stripe SDK
  └─ trial clinics → 403
```

### 2.4 Import flow (opt-in policies)

```
POST /api/v1/marketplace/acquisitions/{id}/import  (perm_marketplace_download)
  body:
    include_policies:              bool
    accepted_policy_attribution:   bool  (required true when include_policies=true)
    relink_existing_policy_ids:    map[int]uuid  (index → existing local policy)

  ImportPlan:
    1. Create tenant form via FormImporter
       - Follows forms invariant: create form + draft → replace fields → publish → v1.0
       - Tags inherited + 'marketplace' appended for traceability
    2. For each bundled PackagePolicy (index i):
       - If relink_existing_policy_ids[i] present → LinkFormToPolicy to existing
       - Else if include_policies → ImportPolicy (creates tenant policy + published
         version + clauses, stamped with source_marketplace_version_id)
         → LinkFormToPolicy
       - Else → skip
    3. Mark acquisition:
       - imported_form_id = new form id
       - policy_import_choice IN 'imported'|'relinked'|'skipped'
       - policy_attribution_accepted_at = now (if included)
```

### 2.5 Stripe webhook (no auth, signature-verified)

```
POST /api/v1/marketplace/webhooks/stripe           (no auth; raw Chi handler)
  body: Stripe event payload
  Stripe-Signature header → VerifyAndParseWebhook
  dedupe: MarkStripeEventProcessed (event_id PK on stripe_events_processed)
  dispatch:
    payment_intent.succeeded → FulfillAcquisitionByPaymentIntent
      (pending → active, denorm fees recorded)
    charge.refunded → RefundAcquisitionByPaymentIntent
      (active → refunded; imported forms retained)
    account.updated → UpdatePublisherStripeConnect(charges_enabled)
```

Refund window is enforced via Stripe's own 7-day refund allowance. Buyer self-serves via Stripe's receipt (no Salvia UI needed); webhook flips our state. Imported forms are NOT revoked — clinical notes may reference them.

---

## 3. Data Model

### 3.1 Schema overview (migrations `00019` + `00020`)

```
publisher_accounts
  clinic_id UNIQUE, display_name, verified_badge,
  authority_type CHECK ('salvia','authority'),
  authority_granted_by FK → publisher_accounts, authority_granted_at,
  stripe_connect_account_id, stripe_onboarding_complete,
  status CHECK ('pending','active','suspended')

marketplace_listings
  publisher_account_id FK, vertical, name, slug UNIQUE,
  short_description, long_description, tags TEXT[],
  bundle_type CHECK ('bundled','form_only'),
  pricing_type CHECK ('free','paid'), price_cents, currency,
  status CHECK ('draft','under_review','published','suspended','archived'),
  search_vector TSVECTOR (trigger-maintained),
  preview_field_count, download_count, rating_count, rating_sum,
  published_at, archived_at,
  CHECK ((free AND price_cents IS NULL) OR (paid AND price_cents IS NOT NULL))

marketplace_versions
  listing_id, version_major, version_minor UNIQUE,
  change_type CHECK ('minor','major'), change_summary,
  package_payload JSONB, payload_checksum, field_count,
  source_form_version_id FK → form_versions,
  status CHECK ('active','deprecated'),
  published_at, published_by FK → staff

marketplace_version_fields                 -- relational mirror of package_payload.fields
  marketplace_version_id ON DELETE CASCADE, position, title, type,
  config JSONB, ai_prompt, required, skippable,
  allow_inference, min_confidence DECIMAL(4,2)

marketplace_acquisitions
  listing_id, marketplace_version_id, clinic_id, acquired_by FK → staff,
  acquisition_type CHECK ('free','purchase'),
  stripe_payment_intent_id, amount_paid_cents, platform_fee_cents, currency,
  status CHECK ('pending','active','refunded'),
  imported_form_id FK → forms,
  policy_import_choice CHECK ('imported','skipped','relinked'),
  policy_attribution_accepted_at,
  PARTIAL UNIQUE (listing_id, clinic_id) WHERE status='active'

marketplace_reviews
  listing_id, acquisition_id (entitlement gate), clinic_id, staff_id,
  rating CHECK BETWEEN 1 AND 5, body,
  status CHECK ('published','hidden','removed'),
  UNIQUE (listing_id, clinic_id)          -- one review per clinic per listing

marketplace_tags                           -- normalised taxonomy
marketplace_listing_tags                   -- join
marketplace_update_notifications           -- upgrade fan-out
  acquisition_id, clinic_id, new_version_id,
  notification_type CHECK ('minor_update','major_upgrade'), seen_at

stripe_events_processed                    -- webhook idempotency
  event_id PK, event_type, processed_at

staff
  + perm_marketplace_manage BOOLEAN         -- migration 00019
  + perm_marketplace_download BOOLEAN       -- migration 00019

policies
  + source_marketplace_version_id UUID NULL -- migration 00020; soft edit warning
```

### 3.2 Package envelope

`marketplace_versions.package_payload JSONB` carries the full portable form package.

```json
{
  "meta": {
    "schema_version":          "1",
    "form_version":            "2.3",
    "salvia_compatible_from":  "1.0",
    "vertical":                "veterinary",
    "bundle_type":             "bundled",
    "policy_attribution":      "Policy content is provided by the publisher under their own license.",
    "published_at":            "2026-04-19T12:00:00Z",
    "checksum":                "a3f5b2c9e1d8..."
  },
  "listing": {
    "name": "Surgical Consent",
    "description": "...",
    "tags": ["surgical-consent"],
    "overall_prompt": "...",
    "policy_dependency_count": 1
  },
  "fields": [
    { "position": 1, "title": "Procedure", "type": "text",
      "config": {}, "ai_prompt": "...", "required": true,
      "skippable": false, "allow_inference": false, "min_confidence": 0.85 }
  ],
  "policies": [
    {
      "name": "Surgical Consent Policy",
      "description": "...",
      "content": [ /* AppFlowy block array — opaque */ ],
      "clauses": [
        { "block_id": "b1", "title": "Owner must acknowledge risks", "parity": "high" }
      ]
    }
  ]
}
```

Block IDs are preserved verbatim through the package → import cycle so form extraction + policy-alignment workflows keep functioning post-import.

`form_only` packages omit `policies[]`. The consumer UI surfaces `policy_dependency_count > 0 AND bundle_type='form_only'` as a dependency warning but the import still works.

---

## 4. Permissions

### 4.1 Staff-level

Two new permissions in `staff` table:
- `perm_marketplace_download` — default on for `super_admin`, `admin`, `vet`, `vet_nurse`
- `perm_marketplace_manage`   — default on for `super_admin` only

Both embedded in JWT `Claims.Perms` and enforced via Huma operation middleware `mw.RequirePermissionHuma`.

### 4.2 Authority-level (inside service)

Permissions above gate which staff can call marketplace-management endpoints. The AuthorityType gate is enforced inside service methods (`GrantBadge`, `RevokeBadge`, `SuspendListing`) based on the caller's `publisher_accounts.authority_type`.

Summary of authority-scoped actions:

| Action | Required authority |
|---|---|
| Grant/revoke verified_badge (own vertical) | `authority_type IN ('salvia','authority')` |
| Grant/revoke `authority_type = 'authority'` on another publisher | `authority_type = 'salvia'` |
| Revoke previously-granted badge on any publisher | `authority_type = 'salvia'` |
| Revoke own previous grant (matches `authority_granted_by`) | `authority_type IN ('salvia','authority')` |
| Suspend a listing (`status='suspended'`) | `authority_type = 'salvia'` |

### 4.3 Clinic-status gates

| Action | Required clinic.status |
|---|---|
| Register as publisher | `active` (trial/suspended → 403) |
| Publish listing / version | `active` or `grace_period` |
| Acquire free listing | `trial`, `active`, or `grace_period` |
| Purchase paid listing | `active` or `grace_period` (trial → 403) |

### 4.4 Tenant-scope rules (CLAUDE.md)

| Table | Tenant-scoped? | `clinic_id` in WHERE? |
|---|---|---|
| `marketplace_listings`, `marketplace_versions`, `marketplace_version_fields`, `marketplace_tags`, `marketplace_listing_tags` | No (global) | No |
| `publisher_accounts` | Scoped via `clinic_id` col | Yes for own-publisher lookups |
| `marketplace_acquisitions`, `marketplace_reviews`, `marketplace_update_notifications` | Scoped | **Yes — always** |
| `stripe_events_processed` | Not scoped (global dedupe) | No |

---

## 5. Search & Sort

### 5.1 tsvector, not Elasticsearch

Postgres full-text search (tsvector) is sized correctly for the NZ veterinary market (hundreds to low-thousands of listings). Moving to Elasticsearch or Typesense would be warranted beyond ~50,000 listings.

`marketplace_listings.search_vector` populated by trigger `marketplace_listings_search_vector()` on `BEFORE INSERT OR UPDATE OF name, tags, short_description, long_description`. Weights:
- `A`: name, tags (normalised array → string)
- `B`: short_description
- `C`: long_description

Public browse combines `websearch_to_tsquery('english', $1)` with filter predicates. `websearch_to_tsquery` is safer than `to_tsquery` for user-typed input (handles quoted phrases, `OR`, `-negation`).

### 5.2 Bayesian rating sort

`ORDER BY rating = (5.0 * 3.0 + rating_sum) / (5 + rating_count) DESC`

Prior count `C=5`, prior mean `m=3.0`. Brand-new listings score 3.0 until ≥5 reviews smooth it toward the true mean. Sort is computed on the fly; no materialised column needed.

---

## 6. Stripe Connect Express

**Tier: Express.** Stripe hosts KYC/payout onboarding; Salvia controls `application_fee_amount`. Matches the pattern used by Substack, Patreon-class platforms.

**Revenue split:**
```
platform_fee_pct = CASE
    WHEN publisher.authority_type IN ('salvia','authority') THEN 0
    ELSE MarketplacePlatformFeePct  -- default 30 via config
END
application_fee_cents = floor(price_cents * platform_fee_pct / 100)
```

**Webhook handling** (in `service.HandleStripeWebhook`):
- Signature verification via `stripe.webhook.ConstructEvent(payload, sig, secret)`
- Dedupe via `stripe_events_processed.event_id` (INSERT ... ON CONFLICT DO NOTHING)
- Dispatch by event type:
  - `payment_intent.succeeded` → fulfill acquisition
  - `charge.refunded` → mark refunded (keep imported_form)
  - `account.updated` → set `stripe_onboarding_complete` flag

Refund: 7-day window by Stripe default, buyer self-serves via Stripe portal/receipt. No custom Salvia refund UI.

---

## 7. Upgrade Notifications

When a publisher creates a new marketplace version, the service calls `CreateUpgradeNotificationsForVersion(listing_id, new_version_id, type)` inside the publish transaction. This inserts one row per active acquisition.

Consumers query `GET /api/v1/marketplace/my/notifications` for unread rows. `POST /my/notifications/{id}/seen` flags them dismissed.

**SSE broker extension (future):** The existing `notifications/broker.go` listens on `salvia_note_events`. To surface marketplace upgrades in real time, either (a) extend the broker's `Event` struct + listen on a second channel `salvia_marketplace_events`, or (b) reuse the note-events channel with a discriminator field. Phase 1 relies on polling via the REST endpoint; live SSE is a future refinement.

---

## 8. File Layout

```
/internal/marketplace/
    repository.go              -- pgx CRUD for all 9 tables + webhook helpers
    service.go                 -- core types, ServiceConfig, EnsurePublisher,
                                   CreateListing, PublishVersion, PublishListing,
                                   Acquire, Import (opt-in policies),
                                   ListMyAcquisitions, GetListingBySlug, GetVersion,
                                   BrowseListings, + status/fee helpers
    service_publisher.go       -- RegisterPublisher, ListMyPublisherListings,
                                   PublishListingByOwner
    service_browse.go          -- BrowseListings (vertical-scoped)
    service_reviews.go         -- CreateReview, ListReviews
    service_notifications.go   -- ListMyUpgradeNotifications, MarkNotificationSeen
    service_badges.go          -- GrantBadge, RevokeBadge, SuspendListing
    service_purchase.go        -- StartPublisherOnboarding, Purchase,
                                   HandleStripeWebhook
    stripe_client.go           -- StripeSDKClient (stripe-go/v82 wrapper)
    handler.go                 -- public browse, acquire, import, publisher CRUD
    handler_phase2_3.go        -- reviews, notifications, badges, suspend, Stripe
    routes.go                  -- Huma + Chi route registration
    fake_repo_test.go          -- in-memory fake repo + fake adapters
    service_test.go            -- Phase 1 flows
    service_phase23_test.go    -- bundled policy, trial gates, fee calc, suspend,
                                   badge grant scope
```

Wired into `internal/app/app.go` via adapters:
- `marketplaceSnapshotAdapter` (FormSnapshotter)
- `marketplacePolicySnapshotAdapter` (PolicySnapshotter)
- `marketplaceImporterAdapter` (FormImporter, includes LinkFormToPolicy)
- `marketplacePolicyImporterAdapter` (PolicyImporter)
- `marketplacePolicyNamerAdapter` (PolicyNamer)
- `marketplaceClinicInfoAdapter` (ClinicInfoProvider)
- `StripeSDKClient` constructed from config (nil when no secret key)

---

## 9. Implementation Invariants

**Draft-then-publish on import.** The forms module enforces "every form has a draft at creation time." Marketplace import obeys this via the importer adapter: `CreateForm` (auto-creates draft) → `UpdateDraft` (replaces fields) → `PublishForm` (freezes to v1.0). Never bypasses.

**Block ID preservation.** Policy snapshots carry clause `block_id` values verbatim. Import creates tenant clauses with the same `block_id`s so the AppFlowy editor content references stay valid and form-alignment checks still pass.

**Source stamping for audit.** Tenant policies imported from marketplace carry `source_marketplace_version_id` so the UI can warn the user when they edit: *"This policy was imported from marketplace; edits may affect form alignment coverage."*

**Duplicate bundling.** If publisher bundles same-name policies across multiple listings, each import creates a separate tenant `policies` row. Consumer can use `relink_existing_policy_ids` to dedupe locally.

**Refund keeps the form.** On `charge.refunded`, `acquisition.status = 'refunded'` but `imported_form_id` is retained. Clinical notes may reference the form — revoking it would orphan audit trails.

**Fee rate is dynamic.** Platform fee is computed per-purchase from current `publisher.authority_type`; not stored on `marketplace_acquisitions` at create time (only on webhook fulfillment as `platform_fee_cents` for historical accuracy).

---

## 10. Environment Variables

```
MARKETPLACE_PLATFORM_FEE_PCT         default 30      // % charged on regular publishers
MARKETPLACE_POLICY_ATTRIBUTION       default ""      // license notice carried in package
STRIPE_SECRET_KEY                    required for paid listings (empty = disabled)
STRIPE_WEBHOOK_SECRET                required for webhook signature verification
```

Migration `00020` seeds:
- `clinics` row `id = 00000000-0000-0000-0000-000000000001`, slug `salvia-platform`
- `publisher_accounts` row `id = 00000000-0000-0000-0000-000000000002`, authority_type `salvia`

Salvia super-admin staff is NOT seeded by migration — bootstrap by creating a `staff` row for that clinic with `perm_marketplace_manage=true` (ops-level action).

---

## 11. Tests

- `service_test.go` — listing validation, acquire/import Phase 1 golden paths, cross-tenant rejection, paid preview locking, Bayesian average, checksum determinism.
- `service_phase23_test.go` — trial clinic can't publish/purchase, authority publishers pay 0% fee, bundled policy import round-trip, attribution acknowledgment required, Salvia-only suspend, authority can't self-elevate to authority_type grant.

11 + 7 = 18 unit tests, all green via `go test ./internal/marketplace/...`. Lint clean via `golangci-lint run ./internal/marketplace/...`.

---

## 12. Phase Roadmap Reference

| Phase | Status | Scope |
|---|---|---|
| **1 (MVP)** | ✅ Shipped | Salvia-curated listings, free acquire + import, admin-only publish |
| **1.5 (Bundling)** | ✅ Shipped | Opt-in policy bundling, `bundle_type`, simplified authority enum `salvia|authority` |
| **2 (Self-serve + reviews + upgrades)** | ✅ Shipped | Self-registration, publisher-owned listing CRUD, reviews, upgrade notifications, trial gates, vertical-scoped browse |
| **3 (Stripe Connect + badges + suspend)** | ✅ Shipped | Express Connect onboarding, Payment Intent flow with dynamic fee, webhook routing + dedupe, badge grant/revoke with authority scope, Salvia-only suspend |
| **4+ (nice-to-have)** | Planned | Subscription-per-library listing tier, marketplace-wide bundle subscription, publisher analytics dashboard, live SSE for upgrade banners |

---

## 13. UI ↔ API Flow Mapping

Every user-facing marketplace action maps to a concrete endpoint call. Grouped
by persona. Flutter client uses the existing JWT + `AuthenticateHuma` middleware
for every call below except the Stripe webhook (server-to-server, signature
verified).

### 13.1 Consumer (clinic staff — wants forms)

#### Browse marketplace

```
UI event                                 API call
─────────────────────────────────────────────────────────────────────
[open "Marketplace" tab]                 GET /api/v1/marketplace/listings
                                             ?sort=newest&limit=20&offset=0
                                         ← auto-scoped to clinic.vertical server-side
                                         ← returns listings + total + publisher meta

[type in search box]                     debounce 300ms → same endpoint with ?q=...
[toggle "Verified only"]                 same endpoint with ?verified_only=true
[toggle "Free / Paid"]                   &pricing_type=free|paid
[change sort: rating/downloads/newest]   &sort=rating|downloads|newest
[pagination click]                       &offset=20
```

#### View listing detail + preview

```
[tap listing card]                       GET /api/v1/marketplace/listings/{slug}
                                         ← returns listing + latest_version
                                         ← for paid: first N fields unlocked, rest
                                           have `"locked": true`

[see reviews tab]                        GET /api/v1/marketplace/listings/{id}/reviews
                                             ?limit=20&offset=0

[view older version]                     GET /api/v1/marketplace/listings/{id}
                                              /versions/{version_id}
```

#### Acquire free form + import

```
[click "Add to my clinic"] (free)        POST /api/v1/marketplace/listings/{id}/acquire
                                         ← returns { id: <acquisition_id>, status:"active" }
                                         ← trial clinic OK for free

[tap "Import now"]                       POST /api/v1/marketplace/acquisitions/{aid}/import
                                         body: {
                                           include_policies: true,
                                           accepted_policy_attribution: true
                                         }
                                         ← returns imported_form_id

Import modal surface BEFORE the POST:
  "This form is bundled with N policies.
   [✓] Import policies too (I accept attribution)
   [ ] Link to my existing policy: [dropdown of local policies per bundle entry]
   [ ] Skip policies (I'll link manually later)"
```

#### Purchase paid form

```
[click "Buy NZ$10"] (paid)               POST /api/v1/marketplace/listings/{id}/purchase
                                         ← returns { client_secret, payment_intent_id,
                                                     acquisition_id, amount_cents }
                                         ← trial clinic → 403 (UI: "upgrade to buy")

[Stripe SDK confirm]                     stripe.confirmPayment(client_secret, …)
                                         ← client-side SDK; 3DS handled natively

[Stripe webhook (backend)]               Stripe → POST /api/v1/marketplace/webhooks/stripe
                                         ← server flips acquisition.status pending→active

UI polls until active:
[poll every 3s × 10]                     GET /api/v1/marketplace/my/acquisitions
                                         → status='active' → show "Import" button
```

#### List my acquired forms

```
[open "My marketplace" tab]              GET /api/v1/marketplace/my/acquisitions
                                             ?limit=20&offset=0
                                         ← entitlements with status + imported_form_id
```

#### Review a form

```
[from "My acquisitions" → "Write review"]
                                         POST /api/v1/marketplace/acquisitions/{aid}/reviews
                                         body: { rating: 1..5, body: "..." }
                                         ← one review per clinic per listing
                                         ← gated on active acquisition server-side
```

#### Upgrade banner

```
[app load / periodic poll]               GET /api/v1/marketplace/my/notifications?limit=20
                                         ← unread rows { id, new_version_id, type }

[banner "Update to v2.0"]
  • "Update" →                          POST /api/v1/marketplace/listings/{id}/acquire (free)
                                         OR POST /...listings/{id}/purchase (paid)
                                         → POST /acquisitions/{new_aid}/import
  • "Remind me later" →                  POST /api/v1/marketplace/my/notifications
                                              /{notification_id}/seen
```

### 13.2 Publisher (clinic wants to publish)

#### Self-register as publisher

```
[Settings → Marketplace → "Become a publisher"]
                                         POST /api/v1/marketplace/publishers
                                         body: { display_name, bio, website_url }
                                         ← returns publisher_account_id
                                         ← trial clinic → 403

UI: button hidden for users without perm_marketplace_manage.
```

#### Create + publish a listing

```
[click "Create listing"]                 POST /api/v1/marketplace/listings
                                         body: {
                                           publisher_account_id,
                                           vertical, name, slug,
                                           short_description,
                                           bundle_type: "bundled" | "form_only",
                                           pricing_type: "free" | "paid",
                                           price_cents (if paid),
                                           preview_field_count
                                         }
                                         ← status='draft'

[pick source form → "Publish v1.0"]
                                         POST /api/v1/marketplace/listings/{id}/versions
                                         body: {
                                           source_form_id,
                                           change_type: "major",
                                           change_summary
                                         }
                                         ← snapshots form + (if bundled) policies
                                         ← computes checksum, writes version row

[click "Go live"]                        POST /api/v1/marketplace/listings/{id}/publish
                                         ← draft → published
                                         ← requires ≥1 version
```

#### Publish new version (update)

```
[from listing detail → "New version"]    POST /api/v1/marketplace/listings/{id}/versions
                                         body: { source_form_id, change_type,
                                                 change_summary }
                                         ← enqueues upgrade notifications for
                                           all existing active acquirers
```

#### My listings dashboard

```
[Settings → Marketplace → "My listings"]
                                         GET /api/v1/marketplace/my/listings
                                             ?limit=20&offset=0
                                         ← publisher's own listings + denorm counts
```

#### Stripe Connect onboarding (paid listings)

```
[see "Set up payouts" banner on paid listing]
                                         POST /api/v1/marketplace/publishers/{pid}
                                              /stripe-onboarding
                                         body: {
                                           email, country: "NZ",
                                           refresh_url, return_url  // Flutter deep links
                                         }
                                         ← returns onboarding_url

[redirect to onboarding_url]             Stripe-hosted KYC (leaves app)
[user completes KYC on Stripe]
[Stripe redirects to return_url]         Flutter intercepts deep link

[Stripe webhook (backend)]               account.updated →
                                         stripe_onboarding_complete = true

UI on return: GET /my/listings to refresh state
```

### 13.3 Authority body (NZVA-like)

#### Grant verified_badge to a clinic in own vertical

```
[internal tool → "Verify clinic"]        POST /api/v1/marketplace/publishers/{pid}/badge
                                         body: { verified_badge: true }
                                         ← server checks caller.authority_type IN
                                           ('salvia','authority')
                                         ← cross-vertical target → 403

[revoke earlier grant]                   DELETE /api/v1/marketplace/publishers/{pid}/badge
                                         ← caller must be original grantor OR Salvia
```

### 13.4 Salvia platform admin

#### Grant authority_type to NZVA

```
[admin console → "Elevate to authority"] POST /api/v1/marketplace/publishers/{pid}/badge
                                         body: {
                                           verified_badge: true,
                                           authority_type: "authority"
                                         }
                                         ← only authority_type='salvia' grantor permitted
```

#### Suspend a bad listing

```
[listing page → "Suspend"]               POST /api/v1/marketplace/listings/{id}/suspend
                                         ← only authority_type='salvia' permitted
                                         ← listing disappears from browse
                                           (status != 'published')
```

### 13.5 Full sequence — paid listing with bundled policy

```
STEP                          UI                             BACKEND
─────────────────────────────────────────────────────────────────────
1  Open marketplace           listings grid                  GET /listings
2  Click listing              detail + locked preview        GET /listings/{slug}
3  Click "Buy"                confirm modal                  POST /listings/{id}/purchase
                                                             ← client_secret
4  Stripe SDK confirms        native 3DS sheet               stripe.confirmPayment (client SDK)
5  Webhook                    (none)                         Stripe → POST /webhooks/stripe
                                                             ← acquisition: pending→active
6  App polls                  status badge flips             GET /my/acquisitions
7  Click "Import"             bundle choice modal:
                                [✓] Import form
                                [✓] Import policies
                                    (attribution required)
                                [ ] Relink: Policy A → dropdown
8  Submit import              loading spinner                POST /acquisitions/{id}/import
                                                             body: include_policies=true,
                                                                   accepted_policy_attribution=true
                                                             ← imported_form_id
9  Navigate to forms          new form visible               GET /api/v1/forms (existing)
10 Later — publisher ships v2 (passive)                      POST /listings/{id}/versions
                                                             (enqueues notifs)
11 App poll                   upgrade banner                 GET /my/notifications
12 User taps "Update"         dismisses or re-purchases      POST /my/notifications/{id}/seen
                                                             → may loop back to step 3
13 User writes review         modal with stars+text          POST /acquisitions/{id}/reviews
                                                             ← rating rolled into listing
```

### 13.6 UI-facing rules

1. **Hide marketplace tab entirely** when caller lacks both `perm_marketplace_download` AND `perm_marketplace_manage`.
2. **Hide "Create listing" / "Become publisher"** without `perm_marketplace_manage`.
3. **Paid-button 403 on trial** — trial + paid → show upgrade CTA, not "Buy."
4. **Locked fields in paid preview** — grayed card with title+type only; no config, no ai_prompt. Tooltip: "Buy to unlock."
5. **Import bundle modal is mandatory for bundled listings with policies** — never one-click import without surfacing policy decision.
6. **Attribution checkbox required** when `include_policies=true` — backend returns 403 without it.
7. **Show authority badge prominently** on listing cards when publisher has `verified_badge=true` AND `authority_type='authority'` (e.g. "NZVA Endorsed").
8. **Suspended listings disappear from browse** — no 410/hidden-state UI; server filters them out.
9. **SSE live updates are future** — poll `/my/notifications` every ~60s on app focus for now.

---

## 14. Cross-ref

| Topic | File |
|---|---|
| Forms module | `docs/forms.md` |
| Policy module | `docs/policy.md` |
| Auth & permissions | `docs/auth.md` |
| Database conventions | `docs/database.md` |
| Compliance | `docs/compliance.md` |
| Testing | `docs/testing.md` |
| Huma middleware | `docs/architecture/huma_middleware.md` |
