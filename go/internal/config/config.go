package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse parses a raw config map (from workflow.Load) into a typed Schema,
// applying defaults for any zero-valued fields. workflowDir is used to resolve
// relative workspace.root paths against the directory containing WORKFLOW.md.
func Parse(raw map[string]any, workflowDir string) (*Schema, error) {
	// 1. Marshal the map back to YAML bytes
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal config map: %w", err)
	}

	// 2. Unmarshal into Schema (this populates fields present in YAML)
	var s Schema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// 3. Apply defaults for any zero-valued fields
	applyDefaults(&s)

	// 4. Resolve relative workspace.root against workflowDir
	if s.Workspace.Root != "" && !filepath.IsAbs(s.Workspace.Root) && !strings.HasPrefix(s.Workspace.Root, "~/") {
		s.Workspace.Root = filepath.Join(workflowDir, s.Workspace.Root)
	}
	// Normalize workspace root to absolute path
	if s.Workspace.Root != "" && !filepath.IsAbs(s.Workspace.Root) {
		abs, err := filepath.Abs(s.Workspace.Root)
		if err == nil {
			s.Workspace.Root = abs
		}
	}

	// 5. Resolve $VAR environment variable references
	if err := resolveEnvVars(&s); err != nil {
		return nil, fmt.Errorf("resolve env vars: %w", err)
	}

	// 6. Validate required fields
	if err := Validate(&s); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	// 7. Return schema
	return &s, nil
}

// applyDefaults sets default values for zero-valued fields.
func applyDefaults(s *Schema) {
	// Tracker defaults
	if s.Tracker.Kind == "linear" && s.Tracker.Linear.Endpoint == "" {
		s.Tracker.Linear.Endpoint = "https://api.linear.app/graphql"
	}
	if s.Tracker.Kind == "plane" && s.Tracker.Plane.Endpoint == "" {
		s.Tracker.Plane.Endpoint = "https://api.plane.so/api/"
	}
	// Keep endpoint defaults even when kind not set yet (for validation messages)
	if s.Tracker.Linear.Endpoint == "" {
		s.Tracker.Linear.Endpoint = "https://api.linear.app/graphql"
	}
	if s.Tracker.Plane.Endpoint == "" {
		s.Tracker.Plane.Endpoint = "https://api.plane.so/api/"
	}
	// Active and terminal states defaults per SPEC 6.4
	if len(s.Tracker.ActiveStates) == 0 {
		s.Tracker.ActiveStates = []string{"Todo", "In Progress"}
	}
	if len(s.Tracker.TerminalStates) == 0 {
		s.Tracker.TerminalStates = []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"}
	}
	// Polling interval default
	if s.Polling.IntervalMS == 0 {
		s.Polling.IntervalMS = 30000
	}
	// Workspace root default
	if s.Workspace.Root == "" {
		s.Workspace.Root = filepath.Join(os.TempDir(), "symphony_workspaces")
	}
	// Expand ~ in workspace root
	if strings.HasPrefix(s.Workspace.Root, "~/") {
		home, _ := os.UserHomeDir()
		s.Workspace.Root = filepath.Join(home, s.Workspace.Root[2:])
	}
	// Hooks timeout default
	if s.Hooks.TimeoutMS == 0 {
		s.Hooks.TimeoutMS = 60000
	}
	// Agent defaults
	if s.Agent.MaxConcurrent == 0 {
		s.Agent.MaxConcurrent = 10
	}
	if s.Agent.MaxTurns == 0 {
		s.Agent.MaxTurns = 20
	}
	if s.Agent.MaxRetryBackoffMS == 0 {
		s.Agent.MaxRetryBackoffMS = 300000
	}
	// Normalize per-state concurrency keys to lowercase.
	if len(s.Agent.MaxConcurrentByState) > 0 {
		normalized := make(map[string]int, len(s.Agent.MaxConcurrentByState))
		for k, v := range s.Agent.MaxConcurrentByState {
			if v > 0 {
				normalized[strings.ToLower(k)] = v
			}
		}
		s.Agent.MaxConcurrentByState = normalized
	}
	// Codex defaults
	if s.Agent.Codex.Command == "" {
		s.Agent.Codex.Command = "codex app-server"
	}
	if s.Agent.Codex.ApprovalPolicy == "" {
		s.Agent.Codex.ApprovalPolicy = "auto"
	}
	if s.Agent.Codex.TurnTimeoutMS == 0 {
		s.Agent.Codex.TurnTimeoutMS = 3600000
	}
	if s.Agent.Codex.ReadTimeoutMS == 0 {
		s.Agent.Codex.ReadTimeoutMS = 5000
	}
	if s.Agent.Codex.StallTimeoutMS == 0 {
		s.Agent.Codex.StallTimeoutMS = 300000
	}
	// Claude defaults
	if s.Agent.Claude.Command == "" {
		s.Agent.Claude.Command = "claude"
	}
	// Agent kind auto-detect: if agent.kind absent but codex.command present -> "codex"
	if s.Agent.Kind == "" && s.Agent.Codex.Command != "" {
		s.Agent.Kind = "codex"
	}
	// HA defaults
	if s.HA.RaftDir == "" && s.Workspace.Root != "" {
		s.HA.RaftDir = filepath.Join(s.Workspace.Root, "raft")
	}
	// Server defaults
	if s.Server.Host == "" {
		s.Server.Host = "localhost"
	}
}

// resolveEnvVars walks all string fields in the Schema. If a value starts with
// '$', it is replaced with the value of the corresponding environment variable.
// If the env var is unset or empty, a descriptive error is returned.
func resolveEnvVars(s *Schema) error {
	return resolveEnvVarsReflect(reflect.ValueOf(s).Elem(), "")
}

// resolveEnvVarsReflect recursively walks struct fields using reflection.
func resolveEnvVarsReflect(v reflect.Value, path string) error {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			// Skip unexported fields
			if !field.IsExported() {
				continue
			}
			fieldPath := fieldPath(path, field.Name)
			if err := resolveEnvVarsReflect(v.Field(i), fieldPath); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			elemPath := fmt.Sprintf("%s[%d]", path, i)
			if err := resolveEnvVarsReflect(v.Index(i), elemPath); err != nil {
				return err
			}
		}
	case reflect.String:
		val := v.String()
		if strings.HasPrefix(val, "$") {
			envName := val[1:]
			resolved := os.Getenv(envName)
			if resolved == "" {
				return fmt.Errorf("config field %s: environment variable %s is not set", path, envName)
			}
			v.SetString(resolved)
		}
	}
	return nil
}

// fieldPath builds a dot-separated field path from parent and child names.
func fieldPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// Validate checks the Schema for required fields and valid values.
// It collects all validation errors and returns them joined, not fail-fast.
func Validate(s *Schema) error {
	var errs []error

	// tracker.kind must be non-empty and valid
	if s.Tracker.Kind == "" {
		errs = append(errs, fmt.Errorf("tracker.kind is required"))
	} else if s.Tracker.Kind != "linear" && s.Tracker.Kind != "plane" {
		errs = append(errs, fmt.Errorf("tracker.kind %q is not valid; must be \"linear\" or \"plane\"", s.Tracker.Kind))
	}

	// agent.kind must be non-empty and valid
	if s.Agent.Kind == "" {
		errs = append(errs, fmt.Errorf("agent.kind is required"))
	} else if s.Agent.Kind != "codex" && s.Agent.Kind != "claude" {
		errs = append(errs, fmt.Errorf("agent.kind %q is not valid; must be \"codex\" or \"claude\"", s.Agent.Kind))
	}

	// tracker.api_key must be non-empty
	if s.Tracker.APIKey == "" {
		errs = append(errs, fmt.Errorf("tracker.api_key is required"))
	}

	// Tracker-specific validation
	if s.Tracker.Kind == "linear" {
		if s.Tracker.Linear.ProjectSlug == "" {
			errs = append(errs, fmt.Errorf("tracker.linear.project_slug is required when tracker.kind is \"linear\""))
		}
	}
	if s.Tracker.Kind == "plane" {
		if s.Tracker.Plane.WorkspaceSlug == "" {
			errs = append(errs, fmt.Errorf("tracker.plane.workspace_slug is required when tracker.kind is \"plane\""))
		}
		if s.Tracker.Plane.ProjectID == "" {
			errs = append(errs, fmt.Errorf("tracker.plane.project_id is required when tracker.kind is \"plane\""))
		}
	}

	// Agent command must be non-empty
	if s.Agent.Kind == "codex" && s.Agent.Codex.Command == "" {
		errs = append(errs, fmt.Errorf("agent.codex.command is required when agent.kind is \"codex\""))
	}
	if s.Agent.Kind == "claude" && s.Agent.Claude.Command == "" {
		errs = append(errs, fmt.Errorf("agent.claude.command is required when agent.kind is \"claude\""))
	}

	// HA validation
	if s.HA.Enabled {
		if len(s.HA.RaftPeers) == 0 {
			errs = append(errs, fmt.Errorf("ha.raft_peers is required when ha.enabled is true"))
		}
		if s.HA.AdvertiseAddr == "" {
			errs = append(errs, fmt.Errorf("ha.advertise_addr is required when ha.enabled is true"))
		}
	}

	return errors.Join(errs...)
}
