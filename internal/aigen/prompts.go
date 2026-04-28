package aigen

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"text/template"
)

// promptFS embeds the prompt templates and few-shot data files at compile time
// so the binary is self-contained — no filesystem reads at runtime.
//
//go:embed prompts/*.tmpl fewshot/form/*.json fewshot/policy/*.json
var promptFS embed.FS

var (
	tmplOnce sync.Once
	tmplErr  error
	formTmpl *template.Template
	polTmpl  *template.Template
)

func initTemplates() {
	tmplOnce.Do(func() {
		formTmpl, tmplErr = template.New("form_system").
			Funcs(template.FuncMap{"join": strings.Join}).
			ParseFS(promptFS, "prompts/form_system.tmpl")
		if tmplErr != nil {
			tmplErr = fmt.Errorf("aigen.prompts.form: %w", tmplErr)
			return
		}
		polTmpl, tmplErr = template.New("policy_system").
			Funcs(template.FuncMap{"join": strings.Join}).
			ParseFS(promptFS, "prompts/policy_system.tmpl")
		if tmplErr != nil {
			tmplErr = fmt.Errorf("aigen.prompts.policy: %w", tmplErr)
		}
	})
}

// RenderFormPrompt fills the form-generation prompt template with the supplied
// PromptContext.
func RenderFormPrompt(ctx PromptContext) (string, error) {
	initTemplates()
	if tmplErr != nil {
		return "", tmplErr
	}
	var buf bytes.Buffer
	if err := formTmpl.ExecuteTemplate(&buf, "form_system.tmpl", ctx); err != nil {
		return "", fmt.Errorf("aigen.prompts.RenderForm: %w", err)
	}
	return buf.String(), nil
}

// RenderPolicyPrompt fills the policy-generation prompt template with the
// supplied PromptContext.
func RenderPolicyPrompt(ctx PromptContext) (string, error) {
	initTemplates()
	if tmplErr != nil {
		return "", tmplErr
	}
	var buf bytes.Buffer
	if err := polTmpl.ExecuteTemplate(&buf, "policy_system.tmpl", ctx); err != nil {
		return "", fmt.Errorf("aigen.prompts.RenderPolicy: %w", err)
	}
	return buf.String(), nil
}

// LoadFewShotForm returns the embedded form-generation few-shot pack for the
// given vertical. Returns nil (no error) if no pack ships for that vertical —
// the prompt simply degrades gracefully without examples.
func LoadFewShotForm(vertical string) []byte {
	return loadOptional(fmt.Sprintf("fewshot/form/%s.json", vertical))
}

// LoadFewShotPolicy returns the embedded policy-generation few-shot pack for
// the given (vertical, country) combo. Returns nil (no error) if no pack
// ships — the prompt degrades gracefully.
func LoadFewShotPolicy(vertical, country string) []byte {
	return loadOptional(fmt.Sprintf("fewshot/policy/%s_%s.json", vertical, country))
}

func loadOptional(path string) []byte {
	b, err := promptFS.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

// FewShotInventory returns the list of fewshot files that shipped in the
// binary. Useful for ops dashboards / startup health checks.
func FewShotInventory() []string {
	var out []string
	_ = fs.WalkDir(promptFS, "fewshot", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out
}
