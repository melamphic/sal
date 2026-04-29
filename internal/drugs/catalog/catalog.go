// Package catalog loads and serves the system master drug catalog.
//
// The catalog is split per (vertical × country) and shipped as JSON data
// files in this package, embedded at compile time via embed.FS. There is
// no DB representation — updates require a deploy. Clinic-specific custom
// drugs live in clinic_drug_catalog_overrides (mutable, tenant-scoped) and
// are loaded separately by the drugs service.
//
// Each entry carries a stable string ID of the form
// "<vertical>.<country>.<active>.<form>.<strength>" — e.g.
// "vet.NZ.ketamine.injectable.100mgml". IDs are case-sensitive ASCII; the
// loader validates uniqueness on startup.
//
// Permission to mutate this data lives in the deployment process, not in
// any role: catalog drift between clinics is impossible by construction.
//
// Adding a new (vertical, country) — drop a JSON file matching the naming
// convention. The loader picks it up automatically. No code change.
//
// Schedules are stored as opaque strings because the regulator naming
// differs by country (S1/S2/S3 NZ; S2/S3/S4/S7/S8/S9 AU; CD2/CD3 UK;
// CII-CV US). The Controls struct on each entry carries the operational
// rules (witness required, register required) so business logic depends on
// behavior, not on parsing schedule strings.
package catalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Files holds the embedded catalog JSON. Adding a new file just means
// dropping it into this package directory; the embed pattern picks it up.
//
//go:embed *.json
var Files embed.FS

// Entry is one drug in the catalog.
//
// Required fields: ID, Name, Schedule (may be "OTC" / "general_sale" for
// non-prescribed), Form, DefaultUnit. Everything else is optional context
// that surfaces in the UI to speed up data entry.
type Entry struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	ActiveIngredient  string            `json:"active_ingredient,omitempty"`
	Schedule          string            `json:"schedule"`
	RegulatoryClass   string            `json:"regulatory_class,omitempty"` // controlled | prescription | general_sale
	CommonStrengths   []string          `json:"common_strengths,omitempty"`
	Form              string            `json:"form"`
	CommonRoutes      []string          `json:"common_routes,omitempty"`
	CommonDoses       map[string]string `json:"common_doses,omitempty"` // species/keyword -> dose hint
	BrandNames        []string          `json:"brand_names,omitempty"`
	DefaultUnit       string            `json:"default_unit"`
	Notes             string            `json:"notes,omitempty"`
	Controls          Controls          `json:"controls"`
}

// Controls captures the operational rules a clinic must follow for an
// entry — independent of the schedule label. The drugs service reads
// these (not the schedule string) when deciding whether to require a
// witness on administer/dispense or to require register inclusion.
type Controls struct {
	WitnessRequired         bool `json:"witness_required"`
	WitnessesRequiredCount  int  `json:"witnesses_required_count,omitempty"` // typically 1; 2 in some jurisdictions
	RegisterRequired        bool `json:"register_required"`
	StorageRestrictionLevel int  `json:"storage_restriction_level,omitempty"` // 0=none, 1=locked cupboard, 2=double-lock CD safe
}

// File is the on-disk representation of one catalog data file.
type File struct {
	Vertical string  `json:"vertical"`
	Country  string  `json:"country"`
	Version  string  `json:"version"`
	Source   string  `json:"source"`
	Entries  []Entry `json:"entries"`
}

// Loader holds the parsed catalogs in memory. Construct once at startup;
// it's safe for concurrent reads.
type Loader struct {
	mu       sync.RWMutex
	byKey    map[string]map[string]Entry // [vertical:country] -> [entry id] -> entry
	manifest []ManifestEntry              // sorted by (vertical, country)
}

// ManifestEntry summarises one (vertical, country) catalog without the
// per-drug detail. Useful for ops dashboards / health checks.
type ManifestEntry struct {
	Vertical string `json:"vertical"`
	Country  string `json:"country"`
	Version  string `json:"version"`
	Source   string `json:"source"`
	Count    int    `json:"count"`
}

// NewLoader parses every embedded JSON file into a typed catalog.
// Returns an error on:
//   - malformed JSON
//   - duplicate entry IDs within a single (vertical, country)
//   - missing required fields (id, name, schedule, form, default_unit)
//
// Cross-catalog ID overlap is permitted by design — the same active
// ingredient may legitimately appear in vet and dental files with
// different IDs and different controls metadata.
func NewLoader() (*Loader, error) {
	dir, err := Files.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("catalog.NewLoader: read embedded dir: %w", err)
	}

	l := &Loader{byKey: make(map[string]map[string]Entry)}

	for _, d := range dir {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		raw, err := Files.ReadFile(d.Name())
		if err != nil {
			return nil, fmt.Errorf("catalog.NewLoader: read %s: %w", d.Name(), err)
		}
		var f File
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("catalog.NewLoader: parse %s: %w", d.Name(), err)
		}
		if err := validateFile(d.Name(), &f); err != nil {
			return nil, err
		}
		key := keyFor(f.Vertical, f.Country)
		bucket, ok := l.byKey[key]
		if !ok {
			bucket = make(map[string]Entry, len(f.Entries))
			l.byKey[key] = bucket
		}
		for _, e := range f.Entries {
			if _, dup := bucket[e.ID]; dup {
				return nil, fmt.Errorf("catalog.NewLoader: duplicate entry id %q in %s", e.ID, d.Name())
			}
			bucket[e.ID] = e
		}
		l.manifest = append(l.manifest, ManifestEntry{
			Vertical: f.Vertical,
			Country:  f.Country,
			Version:  f.Version,
			Source:   f.Source,
			Count:    len(f.Entries),
		})
	}

	sort.Slice(l.manifest, func(i, j int) bool {
		if l.manifest[i].Vertical != l.manifest[j].Vertical {
			return l.manifest[i].Vertical < l.manifest[j].Vertical
		}
		return l.manifest[i].Country < l.manifest[j].Country
	})

	return l, nil
}

func validateFile(filename string, f *File) error {
	if f.Vertical == "" || f.Country == "" {
		return fmt.Errorf("catalog.validateFile: %s missing vertical or country", filename)
	}
	for i, e := range f.Entries {
		if e.ID == "" {
			return fmt.Errorf("catalog.validateFile: %s entries[%d] missing id", filename, i)
		}
		if e.Name == "" {
			return fmt.Errorf("catalog.validateFile: %s entries[%d] (%s) missing name", filename, i, e.ID)
		}
		if e.Schedule == "" {
			return fmt.Errorf("catalog.validateFile: %s entries[%d] (%s) missing schedule", filename, i, e.ID)
		}
		if e.Form == "" {
			return fmt.Errorf("catalog.validateFile: %s entries[%d] (%s) missing form", filename, i, e.ID)
		}
		if e.DefaultUnit == "" {
			return fmt.Errorf("catalog.validateFile: %s entries[%d] (%s) missing default_unit", filename, i, e.ID)
		}
	}
	return nil
}

func keyFor(vertical, country string) string {
	return normalizeVertical(vertical) + ":" + country
}

// normalizeVertical maps the canonical clinic vertical strings used elsewhere
// in the codebase (domain.VerticalVeterinary = "veterinary",
// VerticalGeneralClinic = "general_clinic") to the short forms baked into the
// catalog filenames + IDs ("vet", "general"). Catalog IDs use the short forms
// because they're embedded in human-visible UUID-like strings; clinic
// identifiers stay long for clarity in handlers + DB. Translation lives here
// so callers don't have to know.
func normalizeVertical(v string) string {
	switch v {
	case "veterinary":
		return "vet"
	case "general_clinic":
		return "general"
	default:
		return v
	}
}

// Entries returns all catalog entries for the given (vertical, country)
// combo. Returns nil (no error) when no catalog ships for that combo;
// callers should treat that as "system catalog empty, only override
// drugs available".
func (l *Loader) Entries(vertical, country string) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	bucket, ok := l.byKey[keyFor(vertical, country)]
	if !ok {
		return nil
	}
	out := make([]Entry, 0, len(bucket))
	for _, e := range bucket {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns one entry by its ID within a (vertical, country). Returns
// nil when missing.
func (l *Loader) Lookup(vertical, country, id string) *Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	bucket, ok := l.byKey[keyFor(vertical, country)]
	if !ok {
		return nil
	}
	if e, ok := bucket[id]; ok {
		return &e
	}
	return nil
}

// Manifest returns a summary of all loaded catalogs. Used by health checks
// and the admin dashboard's "what verticals/countries are configured"
// view.
func (l *Loader) Manifest() []ManifestEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]ManifestEntry, len(l.manifest))
	copy(out, l.manifest)
	return out
}

// IsControlled reports whether the given catalog entry requires register
// inclusion (the operational definition of "controlled" per Salvia, which
// is independent of jurisdiction-specific schedule strings).
func (e *Entry) IsControlled() bool {
	return e.Controls.RegisterRequired
}

// RequiresWitness reports whether administering/dispensing this entry
// requires a witness sign-off.
func (e *Entry) RequiresWitness() bool {
	return e.Controls.WitnessRequired
}
