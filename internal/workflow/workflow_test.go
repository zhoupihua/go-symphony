package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantConfig  map[string]any
		wantPrompt  string
		wantErr     bool
		errContains string
	}{
		{
			name:    "valid file with front matter",
			content: "---\ntracker:\n  kind: linear\n  api_key: $LINEAR_API_KEY\nagent:\n  kind: codex\n---\n\nYou are an AI coding assistant. Work on issue {{.Issue.Identifier}}...\n",
			wantConfig: map[string]any{
				"tracker": map[string]any{
					"kind":    "linear",
					"api_key": "$LINEAR_API_KEY",
				},
				"agent": map[string]any{
					"kind": "codex",
				},
			},
			wantPrompt: "You are an AI coding assistant. Work on issue {{.Issue.Identifier}}...",
		},
		{
			name:       "file without front matter",
			content:    "You are an AI coding assistant.\nNo front matter here.\n",
			wantConfig: map[string]any{},
			wantPrompt: "You are an AI coding assistant.\nNo front matter here.",
		},
		{
			name:       "file with empty front matter",
			content:    "---\n---\nSome prompt content\n",
			wantConfig: map[string]any{},
			wantPrompt: "Some prompt content",
		},
		{
			name:       "empty file",
			content:    "",
			wantConfig: map[string]any{},
			wantPrompt: "",
		},
		{
			name:        "invalid YAML in front matter",
			content:     "---\n[invalid: yaml: stuff\n---\nprompt\n",
			wantErr:     true,
			errContains: "workflow file parse error",
		},
		{
			name:        "front matter is a list not a map",
			content:     "---\n- item1\n- item2\n---\nprompt\n",
			wantErr:     true,
			errContains: "front matter must be a mapping",
		},
		{
			name:    "multiple delimiters only first two delimit front matter",
			content: "---\ntracker:\n  kind: linear\n---\nExtra --- delimiter in prompt\n---\nMore content\n",
			wantConfig: map[string]any{
				"tracker": map[string]any{
					"kind": "linear",
				},
			},
			wantPrompt: "Extra --- delimiter in prompt\n---\nMore content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "WORKFLOW.md")
			if err := os.WriteFile(filePath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			config, prompt, err := Load(filePath)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Load() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if !mapsEqual(config, tt.wantConfig) {
				t.Errorf("Load() config = %v, want %v", config, tt.wantConfig)
			}
			if prompt != tt.wantPrompt {
				t.Errorf("Load() prompt = %q, want %q", prompt, tt.wantPrompt)
			}
		})
	}
}

// mapsEqual does a deep comparison of two map[string]any values.
func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		switch av := va.(type) {
		case map[string]any:
			bv, ok := vb.(map[string]any)
			if !ok || !mapsEqual(av, bv) {
				return false
			}
		default:
			if va != vb {
				return false
			}
		}
	}
	return true
}

// writeWorkflowFile writes a WORKFLOW.md file with valid front matter for
// Store tests. The api_key is set to a literal (not $VAR) so config.Parse
// succeeds without requiring environment variables.
func writeWorkflowFile(t *testing.T, dir, content string) string {
	t.Helper()
	filePath := filepath.Join(dir, "WORKFLOW.md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	return filePath
}

const validWorkflow = "---\ntracker:\n  kind: linear\n  api_key: test-key\n  linear:\n    project_slug: myproj\nagent:\n  kind: codex\n---\n\nYou are an AI coding assistant.\n"

const validWorkflowV2 = "---\ntracker:\n  kind: linear\n  api_key: test-key-v2\n  linear:\n    project_slug: myproj-v2\nagent:\n  kind: codex\n---\n\nUpdated prompt.\n"

const invalidWorkflow = "---\n[invalid: yaml: stuff\n---\nprompt\n"

func TestWorkflowStore_InitialLoad(t *testing.T) {
	dir := t.TempDir()
	path := writeWorkflowFile(t, dir, validWorkflow)

	store, err := NewStore(t.Context(), path)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	cfg, prompt, err := store.Current()
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}

	if cfg.Tracker.Kind != "linear" {
		t.Errorf("Tracker.Kind = %q, want %q", cfg.Tracker.Kind, "linear")
	}
	if cfg.Tracker.APIKey != "test-key" {
		t.Errorf("Tracker.APIKey = %q, want %q", cfg.Tracker.APIKey, "test-key")
	}
	if prompt != "You are an AI coding assistant." {
		t.Errorf("prompt = %q, want %q", prompt, "You are an AI coding assistant.")
	}
}

func TestWorkflowStore_FileChange(t *testing.T) {
	dir := t.TempDir()
	path := writeWorkflowFile(t, dir, validWorkflow)

	store, err := NewStore(t.Context(), path)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Verify initial values.
	cfg, _, _ := store.Current()
	if cfg.Tracker.APIKey != "test-key" {
		t.Fatalf("initial Tracker.APIKey = %q, want %q", cfg.Tracker.APIKey, "test-key")
	}

	// Modify the file (ensure mtime changes by waiting briefly).
	time.Sleep(50 * time.Millisecond)
	writeWorkflowFile(t, dir, validWorkflowV2)

	// Wait for the watcher to detect the change (polls every 1s).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cfg, _, _ = store.Current()
		if cfg.Tracker.APIKey == "test-key-v2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	cfg, prompt, err := store.Current()
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if cfg.Tracker.APIKey != "test-key-v2" {
		t.Errorf("Tracker.APIKey = %q, want %q after file change", cfg.Tracker.APIKey, "test-key-v2")
	}
	if prompt != "Updated prompt." {
		t.Errorf("prompt = %q, want %q after file change", prompt, "Updated prompt.")
	}
}

func TestWorkflowStore_BadFileKeepsLastGood(t *testing.T) {
	dir := t.TempDir()
	path := writeWorkflowFile(t, dir, validWorkflow)

	store, err := NewStore(t.Context(), path)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Verify initial values.
	cfg, _, _ := store.Current()
	if cfg.Tracker.APIKey != "test-key" {
		t.Fatalf("initial Tracker.APIKey = %q, want %q", cfg.Tracker.APIKey, "test-key")
	}

	// Overwrite with invalid content.
	time.Sleep(50 * time.Millisecond)
	writeWorkflowFile(t, dir, invalidWorkflow)

	// Wait for watcher to detect the change and attempt reload.
	time.Sleep(2 * time.Second)

	// Should still have the old values.
	cfg, prompt, err := store.Current()
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if cfg.Tracker.APIKey != "test-key" {
		t.Errorf("Tracker.APIKey = %q, want %q (last-known-good)", cfg.Tracker.APIKey, "test-key")
	}
	if prompt != "You are an AI coding assistant." {
		t.Errorf("prompt = %q, want %q (last-known-good)", prompt, "You are an AI coding assistant.")
	}
}

func TestWorkflowStore_ForceReload(t *testing.T) {
	dir := t.TempDir()
	path := writeWorkflowFile(t, dir, validWorkflow)

	store, err := NewStore(t.Context(), path)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Modify the file.
	writeWorkflowFile(t, dir, validWorkflowV2)

	// Force an immediate reload (no waiting for watcher).
	if err := store.ForceReload(); err != nil {
		t.Fatalf("ForceReload() error = %v", err)
	}

	cfg, prompt, err := store.Current()
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if cfg.Tracker.APIKey != "test-key-v2" {
		t.Errorf("Tracker.APIKey = %q, want %q after ForceReload", cfg.Tracker.APIKey, "test-key-v2")
	}
	if prompt != "Updated prompt." {
		t.Errorf("prompt = %q, want %q after ForceReload", prompt, "Updated prompt.")
	}
}

func TestWorkflowStore_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	path := writeWorkflowFile(t, dir, validWorkflow)

	store, err := NewStore(t.Context(), path)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	// Close the store to stop the watcher goroutine.
	store.Close()

	// Give the goroutine time to exit.
	time.Sleep(200 * time.Millisecond)

	// Store should still return cached values even after cancellation.
	cfg, prompt, err := store.Current()
	if err != nil {
		t.Fatalf("Current() error = %v after context cancellation", err)
	}
	if cfg.Tracker.Kind != "linear" {
		t.Errorf("Tracker.Kind = %q, want %q after cancellation", cfg.Tracker.Kind, "linear")
	}
	if prompt != "You are an AI coding assistant." {
		t.Errorf("prompt = %q, want %q after cancellation", prompt, "You are an AI coding assistant.")
	}

	// Verify no goroutine leak: modify the file and confirm the watcher
	// does NOT pick up the change.
	writeWorkflowFile(t, dir, validWorkflowV2)
	time.Sleep(2 * time.Second)

	cfg, _, _ = store.Current()
	if cfg.Tracker.APIKey == "test-key-v2" {
		t.Error("watcher should not have reloaded after context cancellation, but it did")
	}
}
