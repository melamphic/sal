package extraction

import "fmt"

// ValidateExtractionResponse checks an AI extraction result against the field
// specs that were sent. Returns a non-nil error when required fields are
// missing values or the model returned IDs that weren't in the request.
func ValidateExtractionResponse(specs []FieldSpec, results []FieldResult) error {
	// Index the response by field ID.
	byID := make(map[string]*FieldResult, len(results))
	for i := range results {
		byID[results[i].FieldID] = &results[i]
	}

	// Build the expected ID set for unknown-ID detection.
	expectedIDs := make(map[string]bool, len(specs))
	for _, s := range specs {
		expectedIDs[s.ID] = true
	}

	var problems []string

	for _, s := range specs {
		r, ok := byID[s.ID]
		if !ok {
			problems = append(problems, fmt.Sprintf("field %q (%s) missing from AI response", s.ID, s.Title))
			continue
		}
		if s.Required && (r.Value == nil || *r.Value == "" || *r.Value == "null") {
			problems = append(problems, fmt.Sprintf("required field %q (%s) has no value", s.ID, s.Title))
		}
	}

	for _, r := range results {
		if !expectedIDs[r.FieldID] {
			problems = append(problems, fmt.Sprintf("AI returned unknown field_id %q", r.FieldID))
		}
	}

	if len(problems) > 0 {
		return &ExtractionValidationError{Problems: problems}
	}
	return nil
}

// ValidatePolicyCheckResponse checks that the AI returned a result for every
// clause that was sent, and that every status value is a known enum.
func ValidatePolicyCheckResponse(clauses []PolicyClause, results []ClauseCheckResult) error {
	byID := make(map[string]*ClauseCheckResult, len(results))
	for i := range results {
		byID[results[i].BlockID] = &results[i]
	}

	var problems []string

	for _, c := range clauses {
		r, ok := byID[c.BlockID]
		if !ok {
			problems = append(problems, fmt.Sprintf("clause %q missing from AI response", c.BlockID))
			continue
		}
		if r.Status != "satisfied" && r.Status != "violated" {
			problems = append(problems, fmt.Sprintf("clause %q has invalid status %q", c.BlockID, r.Status))
		}
		if r.Reasoning == "" {
			problems = append(problems, fmt.Sprintf("clause %q has empty reasoning", c.BlockID))
		}
	}

	if len(problems) > 0 {
		return &ExtractionValidationError{Problems: problems}
	}
	return nil
}

// ExtractionValidationError collects all problems found in a single AI response.
type ExtractionValidationError struct {
	Problems []string
}

func (e *ExtractionValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "AI response validation: " + e.Problems[0]
	}
	out := fmt.Sprintf("AI response validation: %d problems", len(e.Problems))
	for _, p := range e.Problems {
		out += "\n  - " + p
	}
	return out
}
