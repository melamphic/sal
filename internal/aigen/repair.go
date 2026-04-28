package aigen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	formschema "github.com/melamphic/sal/internal/forms/schema"
	polschema "github.com/melamphic/sal/internal/policy/schema"
)

// RepairLogEntry records a single deterministic correction the repair pass
// applied to AI output. Stored in AIMetadata for audit and is shown to the
// reviewer as "AI draft was auto-corrected on these fields" so the user is
// never surprised by silently-changed content.
type RepairLogEntry struct {
	Field   string `json:"field"`
	Action  string `json:"action"`
	Details string `json:"details,omitempty"`
}

// RepairForm applies deterministic, no-judgment corrections to a generated
// form so an otherwise-valid payload can still be persisted. Returns the
// list of corrections applied.
//
// Repairs performed (in order):
//
//   - Trim too-long titles / ai_prompts to the max with ellipsis
//   - Trim too-long tags + drop empty tags + trim tag list to MaxTagsPerForm
//   - Renumber positions to 1..N preserving original order (sorted by position
//     ascending; ties broken by original index)
//   - Trim trailing fields that exceed the budget cap or hard cap
//
// Any error that this pass cannot fix is left for the validator to flag.
func RepairForm(f *GeneratedForm, budget FieldBudget) []RepairLogEntry {
	var log []RepairLogEntry

	// Trim trailing fields if over budget / hard cap.
	cap := MaxFieldsPerForm
	if budget.Max > 0 && budget.Max < cap {
		cap = budget.Max
	}
	if len(f.Fields) > cap {
		log = append(log, RepairLogEntry{
			Field:   "fields",
			Action:  "trim_to_cap",
			Details: fmt.Sprintf("%d → %d", len(f.Fields), cap),
		})
		f.Fields = f.Fields[:cap]
	}

	// Trim title / ai_prompt lengths AND patch malformed type-specific
	// configs (select / button_group missing options, slider missing or
	// inverted min/max). Both are recoverable: we can drop in a sane
	// placeholder config and let the user finish wiring the options in
	// the editor — far better than failing the whole generation just
	// because the model fumbled one field's config.
	for i := range f.Fields {
		if utf8Len(f.Fields[i].Title) > MaxFieldTitle {
			old := f.Fields[i].Title
			f.Fields[i].Title = truncRunes(old, MaxFieldTitle-1) + "…"
			_ = old
			log = append(log, RepairLogEntry{
				Field:  fmt.Sprintf("fields[%d].title", i),
				Action: "truncate",
			})
		}
		if utf8Len(f.Fields[i].AIPrompt) > MaxFieldAIPrompt {
			old := f.Fields[i].AIPrompt
			f.Fields[i].AIPrompt = truncRunes(old, MaxFieldAIPrompt-1) + "…"
			_ = old
			log = append(log, RepairLogEntry{
				Field:  fmt.Sprintf("fields[%d].ai_prompt", i),
				Action: "truncate",
			})
		}
		if patched, action := repairFieldConfig(&f.Fields[i]); patched {
			log = append(log, RepairLogEntry{
				Field:   fmt.Sprintf("fields[%d].config", i),
				Action:  action,
				Details: "AI returned malformed config; placeholder substituted — review in editor",
			})
		}
	}

	// Renumber positions to 1..N (stable). This fixes duplicates, gaps, and
	// 0/negative positions in one go.
	if needsRenumber(f.Fields) {
		// Sort by current position ascending, but keep original order for
		// equal positions by carrying the slice index.
		type idx struct {
			pos int
			i   int
		}
		order := make([]idx, len(f.Fields))
		for i, fld := range f.Fields {
			p := fld.Position
			if p < 1 {
				// pin invalid positions at the end of the original sort to keep
				// well-formed entries first
				p = 1<<31 - 1
			}
			order[i] = idx{pos: p, i: i}
		}
		sort.SliceStable(order, func(a, b int) bool {
			if order[a].pos != order[b].pos {
				return order[a].pos < order[b].pos
			}
			return order[a].i < order[b].i
		})
		newFields := make([]GeneratedField, len(f.Fields))
		for n, o := range order {
			f.Fields[o.i].Position = n + 1
			newFields[n] = f.Fields[o.i]
		}
		f.Fields = newFields
		log = append(log, RepairLogEntry{
			Field:   "fields[*].position",
			Action:  "renumber_1..N",
			Details: fmt.Sprintf("%d fields renumbered", len(f.Fields)),
		})
	}

	// Tags: drop empty, trim length, cap count.
	if len(f.Tags) > 0 {
		clean := make([]string, 0, len(f.Tags))
		for _, t := range f.Tags {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if utf8Len(t) > MaxTagLength {
				t = truncRunes(t, MaxTagLength)
			}
			clean = append(clean, t)
		}
		if len(clean) > MaxTagsPerForm {
			clean = clean[:MaxTagsPerForm]
		}
		if !sameStringSlice(clean, f.Tags) {
			f.Tags = clean
			log = append(log, RepairLogEntry{
				Field:  "tags",
				Action: "clean_and_cap",
			})
		}
	}
	return log
}

// RepairPolicy applies deterministic corrections to a generated policy.
//
// Repairs performed (in order):
//
//   - Truncate over-long clause titles / bodies / source_citation
//   - Default missing parity values to "medium"
//   - Append a placeholder content block for every clause whose block_id is
//     not present in content (keeps clauses functional and avoids data loss)
//   - Rename duplicate clause block_ids by appending suffix (-2, -3, …)
//   - Trim clauses past MaxClausesPerPolicy
func RepairPolicy(p *GeneratedPolicy) []RepairLogEntry {
	var log []RepairLogEntry

	// Cap clause count.
	if len(p.Clauses) > MaxClausesPerPolicy {
		log = append(log, RepairLogEntry{
			Field:   "clauses",
			Action:  "trim_to_cap",
			Details: fmt.Sprintf("%d → %d", len(p.Clauses), MaxClausesPerPolicy),
		})
		p.Clauses = p.Clauses[:MaxClausesPerPolicy]
	}

	// Truncate over-long fields + default missing parity + dedupe block ids.
	seenBlock := make(map[string]int)
	for i := range p.Clauses {
		c := &p.Clauses[i]

		if utf8Len(c.Title) > MaxClauseTitle {
			c.Title = truncRunes(c.Title, MaxClauseTitle-1) + "…"
			log = append(log, RepairLogEntry{Field: fmt.Sprintf("clauses[%d].title", i), Action: "truncate"})
		}
		if utf8Len(c.Body) > MaxClauseBody {
			c.Body = truncRunes(c.Body, MaxClauseBody-1) + "…"
			log = append(log, RepairLogEntry{Field: fmt.Sprintf("clauses[%d].body", i), Action: "truncate"})
		}
		if c.SourceCitation != nil && utf8Len(*c.SourceCitation) > MaxClauseSourceCitation {
			truncated := truncRunes(*c.SourceCitation, MaxClauseSourceCitation-1) + "…"
			c.SourceCitation = &truncated
			log = append(log, RepairLogEntry{Field: fmt.Sprintf("clauses[%d].source_citation", i), Action: "truncate"})
		}

		if !polschema.IsValidParity(c.Parity) {
			old := c.Parity
			c.Parity = string(polschema.ParityMedium)
			log = append(log, RepairLogEntry{
				Field:   fmt.Sprintf("clauses[%d].parity", i),
				Action:  "default_to_medium",
				Details: fmt.Sprintf("was %q", old),
			})
		}

		// Dedup block ids
		if seenBlock[c.BlockID] > 0 {
			newID := fmt.Sprintf("%s-%d", c.BlockID, seenBlock[c.BlockID]+1)
			log = append(log, RepairLogEntry{
				Field:   fmt.Sprintf("clauses[%d].block_id", i),
				Action:  "rename_duplicate",
				Details: fmt.Sprintf("%q → %q", c.BlockID, newID),
			})
			c.BlockID = newID
		}
		seenBlock[c.BlockID]++
	}

	// Patch missing block ids in content.
	if len(p.Content) > 0 {
		blocks, err := polschema.ParseContent(p.Content)
		if err == nil {
			present := polschema.CollectBlockIDs(blocks)
			added := 0
			for _, c := range p.Clauses {
				if _, ok := present[c.BlockID]; !ok {
					blocks = append(blocks, polschema.Block{
						ID:   c.BlockID,
						Type: "paragraph",
						Data: json.RawMessage(fmt.Sprintf(`{"text":%q}`, c.Body)),
					})
					present[c.BlockID] = struct{}{}
					added++
				}
			}
			if added > 0 {
				if raw, err := json.Marshal(blocks); err == nil {
					p.Content = raw
					log = append(log, RepairLogEntry{
						Field:   "content",
						Action:  "append_missing_blocks_for_clauses",
						Details: fmt.Sprintf("%d block(s) appended", added),
					})
				}
			}
		}
	}
	return log
}

// ── helpers ──────────────────────────────────────────────────────────────────

// repairFieldConfig validates the field's config against the schema
// registry; on failure for the recoverable types (select/button_group/
// slider) it overwrites Config with a sane placeholder so the form can
// still be saved as a draft. Returns (patched, action-name).
//
// We only auto-fix types where a placeholder is genuinely safe to suggest:
//   - select / button_group: 2 generic options ("Option A", "Option B")
//   - slider: 0–10 range with step 1
//
// Any other config error (e.g., invalid JSON for a free-form type) is left
// for the validator to surface — there's no obvious safe default.
func repairFieldConfig(fld *GeneratedField) (bool, string) {
	t := formschema.FieldType(fld.Type)
	if !formschema.IsValidFieldType(string(t)) {
		// Unknown types are validator-rejected; nothing safe to do here.
		return false, ""
	}
	if err := formschema.ValidateConfig(t, fld.Config); err == nil {
		return false, ""
	}
	switch t {
	case formschema.FieldTypeSelect, formschema.FieldTypeButtonGroup:
		// Title-aware default — give the user something CONTEXTUAL when the
		// AI fumbles config, instead of a flat Yes/No on every malformed
		// select. We keep the universe small (5 cases + a Yes/No fallback)
		// because anything beyond that turns into a poor man's NLU and is
		// better solved by improving the prompt. The user still sees the
		// auto-correction banner and can edit the options in the field card.
		fld.Config = json.RawMessage(defaultOptionsFor(fld.Title))
		return true, "default_options_from_title"
	case formschema.FieldTypeSlider:
		fld.Config = json.RawMessage(`{"min":0,"max":10,"step":1}`)
		return true, "default_slider_range"
	default:
		return false, ""
	}
}

// defaultOptionsFor returns a sensible JSON-encoded options array based on
// the field's title. Used by the repair pass when the AI emitted a
// malformed select / button_group config. The substitutions below cover the
// common clinical-form patterns; everything else falls through to Yes/No.
//
// Editing rule: keep the matchers narrow and case-insensitive substring
// matches. Don't add more cases without first checking whether the prompt
// guidance can be improved instead — repair is a safety net, not a feature.
func defaultOptionsFor(title string) string {
	t := strings.ToLower(title)
	switch {
	case containsAny(t, "consent", "agree", "agreed", "permission", "authoris", "authoriz"):
		return `{"options":[{"label":"Verbal","value":"verbal"},{"label":"Written","value":"written"},{"label":"Refused","value":"refused"}]}`
	case containsAny(t, "severity", "grade", "intensity"):
		return `{"options":[{"label":"Mild","value":"mild"},{"label":"Moderate","value":"moderate"},{"label":"Severe","value":"severe"}]}`
	case containsAny(t, "outcome", "result", "response"):
		return `{"options":[{"label":"Successful","value":"successful"},{"label":"Partial","value":"partial"},{"label":"Unsuccessful","value":"unsuccessful"}]}`
	case containsAny(t, "route"):
		return `{"options":[{"label":"PO","value":"po"},{"label":"SC","value":"sc"},{"label":"IM","value":"im"},{"label":"IV","value":"iv"},{"label":"Topical","value":"topical"},{"label":"Inhaled","value":"inhaled"}]}`
	case containsAny(t, "frequency", "schedule"):
		return `{"options":[{"label":"Once","value":"once"},{"label":"BID","value":"bid"},{"label":"TID","value":"tid"},{"label":"QID","value":"qid"},{"label":"PRN","value":"prn"}]}`
	default:
		return `{"options":[{"label":"Yes","value":"yes"},{"label":"No","value":"no"}]}`
	}
}

// containsAny reports whether s contains any of the supplied lowercase
// substrings. Caller is responsible for lowercasing s.
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func needsRenumber(fields []GeneratedField) bool {
	if len(fields) == 0 {
		return false
	}
	seen := make(map[int]struct{}, len(fields))
	for i, f := range fields {
		if f.Position != i+1 {
			return true
		}
		if _, dup := seen[f.Position]; dup {
			return true
		}
		seen[f.Position] = struct{}{}
		if f.Position < 1 {
			return true
		}
	}
	return false
}

func truncRunes(s string, n int) string {
	if utf8Len(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
