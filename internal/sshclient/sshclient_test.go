package sshclient

import (
	"fmt"
	"testing"
)

func TestApplyConfigDefaults(t *testing.T) {
	cfg := Config{}
	applyConfigDefaults(&cfg)

	if cfg.Port != 22 {
		t.Errorf("Port = %d, want 22", cfg.Port)
	}
	if cfg.ConnectTimeout.Seconds() != 30 {
		t.Errorf("ConnectTimeout = %v, want 30s", cfg.ConnectTimeout)
	}
}

func TestApplyConfigDefaultsPreservesSet(t *testing.T) {
	cfg := Config{Port: 2222}
	applyConfigDefaults(&cfg)

	if cfg.Port != 2222 {
		t.Errorf("Port = %d, want 2222", cfg.Port)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	c := New(Config{Host: "example.com"})
	if c.Config().Port != 22 {
		t.Errorf("Port = %d, want 22", c.Config().Port)
	}
}

func TestBuildCommandNoDir(t *testing.T) {
	got := BuildCommand("ls -la", "")
	if got != "ls -la" {
		t.Errorf("BuildCommand = %q, want %q", got, "ls -la")
	}
}

func TestBuildCommandWithDir(t *testing.T) {
	got := BuildCommand("ls -la", "/tmp/work")
	want := "cd '/tmp/work' && ls -la"
	if got != want {
		t.Errorf("BuildCommand = %q, want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBaseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/tmp/work/file.txt", "file.txt"},
		{"C:\\Users\\test\\doc.txt", "doc.txt"},
		{"file.txt", "file.txt"},
	}
	for _, tt := range tests {
		got := baseName(tt.input)
		if got != tt.want {
			t.Errorf("baseName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello\nworld", "hello"},
		{"no newline", "no newline"},
		{"\nsecond", ""},
	}
	for _, tt := range tests {
		got := firstLine(tt.input)
		if got != tt.want {
			t.Errorf("firstLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExitCodeFromError(t *testing.T) {
	if code := exitCodeFromError(nil); code != 0 {
		t.Errorf("exitCodeFromError(nil) = %d, want 0", code)
	}
	if code := exitCodeFromError(fmt.Errorf("some error")); code != -1 {
		t.Errorf("exitCodeFromError(generic) = %d, want -1", code)
	}
}

func TestHostKeyCallbackEmpty(t *testing.T) {
	c := New(Config{Host: "example.com"})
	cb, err := c.hostKeyCallback()
	if err != nil {
		t.Fatalf("hostKeyCallback() error = %v", err)
	}
	if cb == nil {
		t.Error("hostKeyCallback() returned nil")
	}
}
