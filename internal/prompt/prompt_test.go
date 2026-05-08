package prompt

import (
	"testing"

	"github.com/ainative/go-symphony/internal/tracker"
)

func intPtr(v int) *int {
	return &v
}

func TestRender_AllVariables(t *testing.T) {
	issue := tracker.Issue{
		ID:          "abc-123",
		Identifier:  "ENG-123",
		Title:       "Fix login bug",
		Description: "Users cannot log in on mobile",
		State:       "open",
		Priority:    intPtr(1),
		Labels:      []string{"bug", "mobile"},
		URL:         "https://linear.app/issue/ENG-123",
		BlockedBy:   []tracker.BlockerRef{{ID: "id-100", Identifier: "ENG-100", State: "In Progress"}},
	}

	tmpl := `Issue: {{.Issue.Identifier}}
Title: {{.Issue.Title}}
Description: {{.Issue.Description}}
State: {{.Issue.State}}
Priority: {{if .Issue.Priority}}{{.Issue.Priority}}{{end}}
Labels: {{join .Issue.Labels ", "}}
URL: {{.Issue.URL}}
Blocked by: {{range $i, $b := .Issue.BlockedBy}}{{if $i}}, {{end}}{{$b.Identifier}}{{end}}
Attempt: {{.Attempt}}`

	got, err := Render(tmpl, issue, 2)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	want := `Issue: ENG-123
Title: Fix login bug
Description: Users cannot log in on mobile
State: open
Priority: 1
Labels: bug, mobile
URL: https://linear.app/issue/ENG-123
Blocked by: ENG-100
Attempt: 2`

	if got != want {
		t.Errorf("Render() =\n%s\nwant:\n%s", got, want)
	}
}

func TestRender_UnknownVariable(t *testing.T) {
	issue := tracker.Issue{Identifier: "ENG-1"}

	tmpl := `Hello {{.NonExistent}}`
	_, err := Render(tmpl, issue, 1)
	if err == nil {
		t.Fatal("Render() expected error for unknown variable, got nil")
	}
}

func TestRender_NoVariables(t *testing.T) {
	issue := tracker.Issue{}
	tmpl := "This is a plain prompt with no variables."

	got, err := Render(tmpl, issue, 1)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if got != tmpl {
		t.Errorf("Render() = %q, want %q", got, tmpl)
	}
}

func TestRender_NilPriority(t *testing.T) {
	issue := tracker.Issue{
		Identifier: "ENG-456",
		Priority:   nil,
	}

	tmpl := `Issue: {{.Issue.Identifier}}, Priority: {{if .Issue.Priority}}{{.Issue.Priority}}{{else}}none{{end}}`
	got, err := Render(tmpl, issue, 1)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	want := "Issue: ENG-456, Priority: none"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestRender_Labels(t *testing.T) {
	issue := tracker.Issue{
		Identifier: "ENG-789",
		Labels:     []string{"feature", "backend", "urgent"},
	}

	tmpl := `Labels: {{join .Issue.Labels ", "}}`
	got, err := Render(tmpl, issue, 1)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	want := "Labels: feature, backend, urgent"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestRender_InvalidTemplate(t *testing.T) {
	issue := tracker.Issue{}

	tmpl := `Hello {{.Issue.Identifier}` // missing closing braces
	_, err := Render(tmpl, issue, 1)
	if err == nil {
		t.Fatal("Render() expected error for invalid template syntax, got nil")
	}
}

func TestRender_AttemptVariable(t *testing.T) {
	issue := tracker.Issue{Identifier: "ENG-001"}

	tmpl := `Retry attempt {{.Attempt}} for {{.Issue.Identifier}}`
	got, err := Render(tmpl, issue, 5)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	want := "Retry attempt 5 for ENG-001"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}
