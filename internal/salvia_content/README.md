# Salvia Content — Prebuilt Forms & Policies (v1)

This directory holds the canonical Salvia-authored forms and policies that ship
with every clinic. The Go backend reads from this tree at clinic-create time
and instantiates the templates as tenant-owned forms / policies, branded
**"Made by Salvia · v1"** with a removable acknowledgement banner.

> **Status:** Spec-grade content. Not yet wired into `sal/internal/forms` or
> `sal/internal/policy`. Loader implementation tracked separately. See
> `SALVIA_PROVIDED_CONTENT.md` (repo root) for the master spec, and
> `MARKETPLACE_BACKLOG.md` for why the marketplace track was shelved in favour
> of this one.

---

## Layout

```
sal/internal/salvia_content/
├── README.md            ← this file
├── loader.go            ← go:embed + Template type
├── types.go             ← form/policy/field/clause types
├── materialise.go       ← installs templates into a fresh clinic
├── *_test.go            ← unit tests
└── data/                ← embedded content (the YAML library)
    ├── _terms.md        ← user-facing disclaimer / scope-of-use
    ├── _schema.md       ← format specification for forms.yaml and policies.yaml
    ├── INDEX.md         ← auto-generated catalogue
    ├── shared/          ← cross-cutting (every clinic, every vertical)
    │   ├── forms/       ← 3 forms
    │   └── policies/    ← 8 policies
    ├── veterinary/      ← 6 forms + 6 policies vet-specific
    ├── dental/          ← 9 forms + 7 policies dental-specific
    ├── general_clinic/  ← 10 forms + 10 policies GP-specific
    └── aged_care/       ← 17 forms + 24 policies aged-care-specific
```

The YAML lives under `data/` so `go:embed data` picks it up. Go's embed
cannot follow symlinks outside the module, so the source-of-truth has to
live inside `sal/`.

A clinic with vertical = `dental` and country = `NZ` receives, on Day 0:
- everything in `shared/`
- everything in `dental/`
- variant blocks tagged `NZ` rendered (other countries' variant text dropped)

= **38 forms + policies installed, all country-localised, all badged.**

## The badge & lifecycle

Every template carries `badge: "Made by Salvia v1"` plus a `currency_date` (the
date the regulator framework was last reviewed). This appears:

- as a chip in the Forms / Policies list in-app
- in the PDF footer/watermark via the existing doc-theme system
  (`feedback_doc_theme_central.md`)
- in the Salvia Library panel banner

A clinic can: **use as-is**, **fork to edit** (badge clears, lineage retained),
or **delete** (kept removed across re-sync; can be re-added from Library panel).

Lineage columns mirror the existing marketplace lineage pattern (migration
`00088_forms_marketplace_lineage.sql`):

- `salvia_template_id` — the canonical id from this directory
- `salvia_template_version` — bumps when this directory updates
- `salvia_template_state` — `default` / `forked` / `deleted`
- `framework_currency_date` — copied from the YAML file

When a template's content changes here and version bumps, clinics still on
`default` see the existing `marketplace_lineage_banner.dart` upgrade UX (relabelled
"Salvia v1.1 available").

## Country localisation

Single template per concept; country variants live INSIDE the file as `_per_country`
blocks where text differs:

```yaml
clauses:
  - id: lawful_basis
    title: Lawful basis for collection
    body_per_country:
      NZ: |
        Information is collected under HIPC 2020 Rule 1 ...
      AU: |
        Information is collected under APP 3 ...
```

The loader picks the clinic's country at install time. See `_schema.md` for full
format.

## Currency commitments

- **6-monthly review** per market for every template in this tree.
- **30-day emergency revision** when a major regulator publication changes
  requirements (e.g. 2026: NZ DCNZ Sedation Standard March 2026 · UK CQC
  registration changes 9 Feb 2026 · AU SIRS guidance Feb 2026 ·
  US HIPAA NPP 16 Feb 2026 · US MDS 3.0 v1.20.1 Oct 2025).
- A `framework_currency_date` is mandatory on every file; CI rejects files
  older than 12 months from current date.

## Editing

These files are the source of truth. Changes here must:

1. Bump the file's `version` field.
2. Update `currency_date` if the change reflects a regulator-framework refresh.
3. Pass YAML lint and the upcoming schema-validator.

A clinical compliance reviewer per vertical signs off before files merge to
main. **This is a launch gate.** Internal review is not enough for the legal
posture (see `_terms.md`).

## Disclaimer summary

Templates are **not legal advice**. They are curated against publicly available
regulator guidance current at the date stamped on the file. The clinic remains
responsible for compliance. Salvia recommends review by a qualified compliance
practitioner before adoption. Once a clinic forks a template, ownership
transfers to the clinic. Full text in `_terms.md`.
