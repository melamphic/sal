// validate-content checks every embedded form and policy YAML for structural
// and content rule violations. Run via: make validate-content
// Exits 1 if any violations are found.
package main

import (
	"fmt"
	"os"

	"github.com/melamphic/sal/internal/salvia_content"
)

func main() {
	errs, err := salvia_content.ValidateAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load error: %v\n", err)
		os.Exit(1)
	}

	if len(errs) == 0 {
		fmt.Println("OK — all forms and policies pass validation.")
		return
	}

	for _, ve := range errs {
		fmt.Fprintf(os.Stderr, "\n[FAIL] %s\n", ve.Path)
		for _, e := range ve.Errors {
			fmt.Fprintf(os.Stderr, "  • %s\n", e)
		}
	}
	fmt.Fprintf(os.Stderr, "\n%d template(s) failed validation.\n", len(errs))
	os.Exit(1)
}
