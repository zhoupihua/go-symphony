// Package workflow loads and parses WORKFLOW.md files containing YAML front
// matter and a prompt template body.
package workflow

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sentinel errors for workflow loading.
var (
	ErrMissingWorkflowFile = errors.New("workflow file not found")
	ErrWorkflowParseError  = errors.New("workflow file parse error")
	ErrFrontMatterNotMap   = errors.New("front matter must be a mapping")
)

// Load reads the file at path, parses optional YAML front matter delimited by
// "---" at the start of the file, and returns the front matter as a config
// map along with the remaining content as the prompt template string.
//
// Format:
//
//	---
//	tracker:
//	  kind: linear
//	---
//
//	You are an AI coding assistant...
//
// Behavior:
//   - File starts with "---\n" → parse content between first and second "---"
//     as YAML; everything after the second "---" is the prompt.
//   - File does NOT start with "---" → entire file is prompt, config is empty map.
//   - Empty file → empty config, empty prompt, no error.
//   - Invalid YAML in front matter → return error.
//   - Front matter that parses to non-map (e.g. a list) → return error.
//   - Multiple "---" delimiters → only first two delimit front matter.
func Load(path string) (config map[string]any, prompt string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrMissingWorkflowFile, err)
	}

	content := string(data)

	// Empty file → empty config, empty prompt, no error.
	if content == "" {
		return map[string]any{}, "", nil
	}

	// File does NOT start with "---\n" → entire file is prompt.
	if !strings.HasPrefix(content, "---\n") {
		return map[string]any{}, strings.TrimSpace(content), nil
	}

	// Find the closing "---" delimiter after the opening one.
	rest := content[len("---\n"):]

	// The closing delimiter may appear at the very start of rest (empty front
	// matter: "---\n---\n...") or after a newline. Check start-of-rest first.
	var fmBytes []byte
	if strings.HasPrefix(rest, "---\n") || rest == "---" {
		// Empty front matter.
		fmBytes = nil
		prompt = strings.TrimPrefix(rest, "---\n")
		prompt = strings.TrimPrefix(prompt, "---")
	} else {
		idx := strings.Index(rest, "\n---")
		if idx < 0 {
			// No closing delimiter found; treat entire file as prompt.
			return map[string]any{}, content, nil
		}
		fmBytes = []byte(rest[:idx])
		prompt = rest[idx+len("\n---"):]
	}

	// Trim the prompt to match the Elixir reference implementation behavior.
	prompt = strings.TrimSpace(prompt)

	// Parse front matter as YAML.
	var raw any
	if err := yaml.Unmarshal(fmBytes, &raw); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrWorkflowParseError, err)
	}

	// Empty front matter → empty config.
	if raw == nil {
		return map[string]any{}, prompt, nil
	}

	// Front matter must be a map.
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("%w: got %T", ErrFrontMatterNotMap, raw)
	}

	return m, prompt, nil
}
