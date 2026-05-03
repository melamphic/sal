package validators

import "strings"

// auStateRules is a table-driven set of state-specific overrides. The
// design rule (per docs/drug-register-compliance-v2.md §3.3): no code
// branches on state. Add a state by adding a row here.
//
// Default = WA-strict. Other states relax by flipping flags off.
type auStateRules struct {
	State                    string
	RequireDispenseWitness   bool
	RequireDestructionReason bool
	// RequirePurposeColumn — QLD-specific: Sch 8 admin in animals
	// requires "purpose of administration" column on the register.
	RequirePurposeColumn bool
}

// auRules indexed by canonical 2-letter state code.
var auRules = map[string]auStateRules{
	"WA": {
		State:                    "WA",
		RequireDispenseWitness:   true,
		RequireDestructionReason: true,
	},
	"NSW": {
		State:                    "NSW",
		RequireDispenseWitness:   false,
		RequireDestructionReason: true,
	},
	"VIC": {
		State:                    "VIC",
		RequireDispenseWitness:   false,
		RequireDestructionReason: true,
	},
	"QLD": {
		State:                    "QLD",
		RequireDispenseWitness:   false,
		RequireDestructionReason: true,
		RequirePurposeColumn:     true,
	},
	"SA": {
		State:                    "SA",
		RequireDispenseWitness:   false,
		RequireDestructionReason: true,
	},
	"TAS": {
		State:                    "TAS",
		RequireDispenseWitness:   false,
		RequireDestructionReason: true,
	},
	"ACT": {
		State:                    "ACT",
		RequireDispenseWitness:   false,
		RequireDestructionReason: true,
	},
	"NT": {
		State:                    "NT",
		RequireDispenseWitness:   false,
		RequireDestructionReason: true,
	},
}

// ruleForState returns the rule set for an AU state. Falls back to the
// strictest (WA) when state is missing or unknown — better to
// over-validate in shadow mode than miss a real violation.
func ruleForState(state string) auStateRules {
	s := strings.ToUpper(strings.TrimSpace(state))
	if r, ok := auRules[s]; ok {
		return r
	}
	return auRules["WA"]
}
