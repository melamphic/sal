package v2

import "html/template"

// commonFuncs is the shared template FuncMap available to every
// report. Keep it small — purely presentational helpers (math,
// pluralisation, formatting) only. Domain-specific helpers stay in
// their own builder file's FuncMap.
func commonFuncs() template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b float64) float64 { return a - b },
		"sub_i": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
		"plural": func(n int, single, plural string) string {
			if n == 1 {
				return single
			}
			return plural
		},
		"len_int": func(slice any) int {
			// html/template already has `len` — this exists for
			// rare cases inside Funcs-mapped scopes that lose the
			// builtin when chained.
			switch s := slice.(type) {
			case []any:
				return len(s)
			default:
				return 0
			}
		},
	}
}
