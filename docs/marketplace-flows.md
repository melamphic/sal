# Marketplace — UI ↔ API Flow Mapping

Companion to `docs/marketplace.md`. Every user-facing marketplace action maps to a concrete endpoint call, grouped by persona. All calls authenticated via JWT (`AuthenticateHuma` middleware) except the Stripe webhook (server-to-server, signature verified).

---

## Persona A — Consumer (clinic staff wants forms)

### A1. Browse marketplace

```
UI event                                 API call
─────────────────────────────────────────────────────────────────────
[open "Marketplace" tab]                 GET /api/v1/marketplace/listings
                                             ?q=&vertical=&pricing_type=&sort=newest
                                             &limit=20&offset=0
                                         ← auto-scoped to clinic.vertical server-side
                                         ← returns listings + total + publisher meta

[type in search box]                     debounce 300ms → same endpoint with ?q=...

[toggle "Verified only"]                 same endpoint with ?verified_only=true
[toggle "Free / Paid"]                   &pricing_type=free|paid
[change sort: rating/downloads/newest]   &sort=rating|downloads|newest
[pagination click]                       &offset=20
```

### A2. View listing detail + preview

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

### A3. Acquire free form

```
[click "Add to my clinic"] (free)        POST /api/v1/marketplace/listings/{id}/acquire
                                         ← returns { id: <acquisition_id>, status:"active" }
                                         ← server-side: trial clinic OK

[tap "Import now"]                       POST /api/v1/marketplace/acquisitions/{aid}/import
                                         body: {
                                           include_policies: true,
                                           accepted_policy_attribution: true
                                         }
                                         ← returns imported_form_id
UI: show modal before POST —
  "This form is bundled with N policies.
   [✓] Import policies too (I accept attribution)
   [ ] Link to my existing policy: [dropdown of local policies per bundle entry]
   [ ] Skip policies (I'll link manually later)"
```

### A4. Purchase paid form

```
[click "Buy NZ$10"] (paid)               POST /api/v1/marketplace/listings/{id}/purchase
                                         ← returns { client_secret, payment_intent_id,
                                                     acquisition_id, amount_cents }
                                         ← trial clinic → 403 (UI: "upgrade to buy")

[Stripe SDK confirm]                     stripe.confirmPayment(client_secret, …)
                                         ← 3DS redirect if needed
                                         ← client-side SDK, no Salvia call

[Stripe webhook (backend)]               Stripe → POST /api/v1/marketplace/webhooks/stripe
                                         ← server flips acquisition.status to 'active'

UI polls OR uses Stripe SDK's returned PaymentIntent status:
[poll] GET /api/v1/marketplace/my/acquisitions → find id → status='active' → "Import" button appears
[OR] subscribe to SSE notification in future; for now poll every 3s × 10
```

### A5. List my acquired forms

```
[open "My marketplace" tab]              GET /api/v1/marketplace/my/acquisitions
                                             ?limit=20&offset=0
                                         ← returns entitlements with status + imported_form_id
```

### A6. Review a form

```
[from "My acquisitions"]
[tap "Write review"]                     POST /api/v1/marketplace/acquisitions/{aid}/reviews
                                         body: { rating: 1..5, body: "..." }
                                         ← one review per clinic per listing
                                         ← gated on active acquisition server-side
```

### A7. Upgrade banner

```
[app load / periodic poll]               GET /api/v1/marketplace/my/notifications?limit=20
                                         ← unread rows { id, new_version_id, type }

[banner click "Update to v2.0"]
  option 1: "Re-import" (create new form)
                                         → pin to new version via:
                                         POST /api/v1/marketplace/listings/{id}/acquire (free)
                                         OR POST /...listings/{id}/purchase (paid)
                                         → POST /acquisitions/{new_aid}/import
  option 2: "Remind me later"
                                         POST /api/v1/marketplace/my/notifications
                                              /{notification_id}/seen
```

---

## Persona B — Publisher (clinic wants to publish)

### B1. Self-register as publisher

```
[Settings → Marketplace → "Become a publisher"]
                                         POST /api/v1/marketplace/publishers
                                         body: { display_name, bio, website_url }
                                         ← returns publisher_account_id
                                         ← trial clinic → 403

UI: require perm_marketplace_manage. Hide button for users without it.
```

### B2. Create first listing

```
[click "Create listing"]                 POST /api/v1/marketplace/listings
                                         body: {
                                           publisher_account_id,  // from B1
                                           vertical, name, slug,
                                           short_description,
                                           bundle_type: "bundled" | "form_only",
                                           pricing_type: "free" | "paid",
                                           price_cents (if paid),
                                           preview_field_count
                                         }
                                         ← status='draft'

[pick source form from dropdown → "Publish v1.0"]
                                         POST /api/v1/marketplace/listings/{id}/versions
                                         body: {
                                           source_form_id,          // caller's form
                                           change_type: "major",
                                           change_summary
                                         }
                                         ← snapshots form + (if bundled) policies
                                         ← computes checksum, writes version row

[click "Go live"]                        POST /api/v1/marketplace/listings/{id}/publish
                                         ← draft → published
                                         ← requires ≥1 version
```

### B3. Publish new version (update)

```
[from listing detail → "New version"]    POST /api/v1/marketplace/listings/{id}/versions
                                         body: { source_form_id, change_type,
                                                 change_summary }
                                         ← enqueues upgrade notifications for all
                                           existing acquirers automatically
```

### B4. My listings dashboard

```
[Settings → Marketplace → "My listings"]
                                         GET /api/v1/marketplace/my/listings
                                             ?limit=20&offset=0
                                         ← publisher's own listings + denorm counts
```

### B5. Stripe Connect onboarding (for paid listings)

```
[see "Set up payouts" banner on paid listing]
                                         POST /api/v1/marketplace/publishers/{pid}
                                              /stripe-onboarding
                                         body: {
                                           email, country: "NZ",
                                           refresh_url, return_url  // Flutter deep links
                                         }
                                         ← returns onboarding_url

[redirect to onboarding_url]             (Stripe-hosted — leaves app)
[user completes KYC on Stripe]
[Stripe redirects to return_url]         Flutter intercepts deep link

[Stripe webhook (backend)]               account.updated →
                                         stripe_onboarding_complete = true

UI on return: GET /api/v1/marketplace/my/listings to refresh state
```

---

## Persona C — Authority body (NZVA)

### C1. Grant verified_badge to a clinic in own vertical

```
[Internal NZVA tool → "Verify clinic"]   POST /api/v1/marketplace/publishers/{pid}/badge
                                         body: { verified_badge: true }
                                         ← server checks caller.authority_type IN
                                           ('salvia','authority')
                                         ← cross-vertical target → 403

[Revoke earlier grant]                   DELETE /api/v1/marketplace/publishers/{pid}/badge
                                         ← caller must be original grantor OR Salvia
```

---

## Persona D — Salvia platform admin (own clinic = salvia-platform)

### D1. Grant authority_type to NZVA

```
[Admin console → "Elevate to authority"] POST /api/v1/marketplace/publishers/{pid}/badge
                                         body: {
                                           verified_badge: true,
                                           authority_type: "authority"
                                         }
                                         ← only salvia grantor permitted
```

### D2. Suspend a bad listing

```
[Listing page → "Suspend"]               POST /api/v1/marketplace/listings/{id}/suspend
                                         ← only authority_type='salvia' permitted
                                         ← listing disappears from browse
                                           (status != 'published')
```

---

## Full buyer journey (paid with bundled policy) — one sequence

```
STEP                          UI                             BACKEND
─────────────────────────────────────────────────────────────────────
1 Open marketplace            []                             GET /listings
2 Click listing               detail + locked preview        GET /listings/{slug}
3 Click "Buy"                 confirm modal                  POST /listings/{id}/purchase
                                                             ← client_secret
4 Stripe SDK confirms         native 3DS sheet               stripe.confirmPayment
5 Webhook                     (none)                         Stripe → POST /webhooks/stripe
                                                             ← acquisition: pending→active
6 App polls                   status badge flips             GET /my/acquisitions
7 Click "Import"              bundle choice modal:
                                [✓] Import form
                                [✓] Import policies (need accept)
                                [ ] Relink: Policy A → (dropdown)
8 Submit import               loading spinner                POST /acquisitions/{id}/import
                                                             body: include_policies=true,
                                                                   accepted_policy_attribution=true
                                                             ← imported_form_id
9 Navigate to forms           new form visible               GET /api/v1/forms (existing)
10 Later — publisher
   ships v2.0                 (passive)                      POST /listings/{id}/versions
                                                             (backend enqueues notifs)
11 App poll                   upgrade banner                 GET /my/notifications
12 User taps "Update"         dismisses or re-purchases      POST /my/notifications/{id}/seen
                                                             → may loop back to step 3
13 User writes review         modal with stars+text          POST /acquisitions/{id}/reviews
                                                             ← rating rolled into listing
```

---

## UI-facing rules to bake in

1. **Hide marketplace tab entirely** when caller lacks both `perm_marketplace_download` AND `perm_marketplace_manage`.
2. **Hide "Create listing" / "Become publisher"** without `perm_marketplace_manage`.
3. **Paid-button 403 on trial** — when trial + paid → show upgrade CTA, not "Buy."
4. **Locked fields in paid preview** — render grayed-out card with title+type only; no config, no ai_prompt. Tooltip: "Buy to unlock."
5. **Import bundle modal is mandatory for bundled listings with policies** — never one-click import without surfacing policy decision.
6. **Attribution checkbox required** when `include_policies=true` — backend returns 403 without it.
7. **Show authority badge prominently** on listing cards when publisher has `verified_badge=true` AND `authority_type='authority'` (e.g. "NZVA Endorsed").
8. **Suspended listings disappear from browse** — no 410/hidden-state UI needed; server filters them out.
9. **SSE live updates are future** — poll `/my/notifications` every ~60s on app focus for now.

---

## Related

- `docs/marketplace.md` — full architecture reference (schema, permissions, Stripe, invariants).
