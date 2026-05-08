package prompt

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/ainative/go-symphony/internal/tracker"
)

// templateData is the data model passed to the template engine.
type templateData struct {
	Issue   tracker.Issue
	Attempt int
}

// Render renders the prompt template with issue variables.
// Unknown variables cause an error (strict mode).
func Render(tmpl string, issue tracker.Issue, attempt int) (string, error) {
	funcMap := template.FuncMap{
		"join": strings.Join,
	}

	t, err := template.New("prompt").Option("missingkey=error").Funcs(funcMap).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse prompt template: %w", err)
	}

	var buf strings.Builder
	data := templateData{Issue: issue, Attempt: attempt}
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute prompt template: %w", err)
	}

	return buf.String(), nil
}
