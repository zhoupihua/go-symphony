package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// SanitizeKey
// ---------------------------------------------------------------------------

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
		want       string
	}{
		{"normal identifier", "ENG-123", "ENG-123"},
		{"special chars", "issue/feature#2", "issue_feature_2"},
		{"spaces", "my issue", "my_issue"},
		{"empty string", "", ""},
		{"only safe chars", "abcXYZ012._-", "abcXYZ012._-"},
		{"unicode chars", "issue\x00tab", "issue_tab"},
		{"dots preserved", "v2.1.0", "v2.1.0"},
		{"underscores preserved", "my_issue", "my_issue"},
		{"hyphens preserved", "my-issue", "my-issue"},
		{"slashes replaced", "a/b/c", "a_b_c"},
		{"hash replaced", "issue#42", "issue_42"},
		{"backslash replaced", `a\b`, "a_b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeKey(tt.identifier)
			if got != tt.want {
				t.Errorf("SanitizeKey(%q) = %q, want %q", tt.identifier, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsUnderRoot
// ---------------------------------------------------------------------------

func TestIsUnderRoot_PathUnderRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "workspace")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := IsUnderRoot(root, sub); err != nil {
		t.Errorf("IsUnderRoot(%q, %q) returned error: %v", root, sub, err)
	}
}

func TestIsUnderRoot_NonExistentPathUnderRoot(t *testing.T) {
	root := t.TempDir()
	// Path does not exist yet, but would be under root if created
	sub := filepath.Join(root, "workspace", "subdir")
	if err := IsUnderRoot(root, sub); err != nil {
		t.Errorf("IsUnderRoot(%q, %q) for non-existent path returned error: %v", root, sub, err)
	}
}

func TestIsUnderRoot_PathEscapesRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	sub := filepath.Join(other, "workspace")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := IsUnderRoot(root, sub); err == nil {
		t.Errorf("IsUnderRoot(%q, %q) should return error for path escaping root", root, sub)
	}
}

func TestIsUnderRoot_DotDotEscapesRoot(t *testing.T) {
	root := t.TempDir()
	// filepath.Join(root, "..") resolves to parent of root
	escapPath := filepath.Join(root, "..")
	if err := IsUnderRoot(root, escapPath); err == nil {
		t.Errorf("IsUnderRoot(%q, %q) should return error for .. path", root, escapPath)
	}
}

func TestIsUnderRoot_SymlinkEscapingRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}

	root := t.TempDir()
	other := t.TempDir()

	// Create a directory inside other
	target := filepath.Join(other, "secret")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside root pointing outside
	link := filepath.Join(root, "escape")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := IsUnderRoot(root, link); err == nil {
		t.Error("IsUnderRoot should detect symlink escaping root")
	}
}

func TestIsUnderRoot_RootItself(t *testing.T) {
	root := t.TempDir()
	if err := IsUnderRoot(root, root); err != nil {
		t.Errorf("IsUnderRoot(%q, %q) should accept root itself", root, root)
	}
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func TestCreate_CreatesDirectory(t *testing.T) {
	root := t.TempDir()

	path, createdNow, err := Create(context.Background(), root, "ENG-123", "", 0)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	expected := filepath.Join(root, "ENG-123")
	if path != expected {
		t.Errorf("Create path = %q, want %q", path, expected)
	}
	if !createdNow {
		t.Error("Create createdNow = false, want true for new workspace")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("workspace directory does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("workspace path is not a directory")
	}
}

func TestCreate_DetectsAlreadyExists(t *testing.T) {
	root := t.TempDir()

	// First create
	_, created1, err := Create(context.Background(), root, "ENG-456", "", 0)
	if err != nil {
		t.Fatalf("first Create returned error: %v", err)
	}
	if !created1 {
		t.Error("first create should report createdNow=true")
	}

	// Second create of same workspace
	path2, created2, err := Create(context.Background(), root, "ENG-456", "", 0)
	if err != nil {
		t.Fatalf("second Create returned error: %v", err)
	}
	if created2 {
		t.Error("second create should report createdNow=false")
	}

	expected := filepath.Join(root, "ENG-456")
	if path2 != expected {
		t.Errorf("second Create path = %q, want %q", path2, expected)
	}
}

func TestCreate_SanitizesIdentifier(t *testing.T) {
	root := t.TempDir()

	path, _, err := Create(context.Background(), root, "issue/feature#2", "", 0)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	expected := filepath.Join(root, "issue_feature_2")
	if path != expected {
		t.Errorf("Create path = %q, want %q (sanitized)", path, expected)
	}
}

func TestCreate_WithPathTraversalIdentifier(t *testing.T) {
	root := t.TempDir()

	// ".." is a dangerous identifier: SanitizeKey keeps dots,
	// but filepath.Join(root, "..") resolves up and IsUnderRoot catches it.
	_, _, err := Create(context.Background(), root, "..", "", 0)
	if err == nil {
		t.Error("Create should reject path traversal identifier")
	}
}

func TestCreate_AfterCreateHook(t *testing.T) {
	root := t.TempDir()

	// Use relative path in hook since cmd.Dir is set to workspace directory.
	// Absolute Windows paths (C:\...) contain backslashes that bash interprets as escapes.
	hook := "echo ok > hook_ran"
	_, createdNow, err := Create(context.Background(), root, "ENG-789", hook, 10*time.Second)
	if err != nil {
		t.Fatalf("Create with hook returned error: %v", err)
	}
	if !createdNow {
		t.Error("Create should report createdNow=true")
	}

	marker := filepath.Join(root, "ENG-789", "hook_ran")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("after_create hook did not run: marker file %q does not exist", marker)
	}
}

func TestCreate_AfterCreateHookFailure_Aborts(t *testing.T) {
	root := t.TempDir()

	hook := "exit 1"
	_, _, err := Create(context.Background(), root, "ENG-HOOKFAIL", hook, 10*time.Second)
	if err == nil {
		t.Error("Create should return error when after_create hook fails")
	}

	// Workspace directory should have been cleaned up
	workspacePath := filepath.Join(root, "ENG-HOOKFAIL")
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Error("workspace directory should be removed after hook failure")
	}
}

// ---------------------------------------------------------------------------
// Remove
// ---------------------------------------------------------------------------

func TestRemove_RemovesDirectory(t *testing.T) {
	root := t.TempDir()

	// Create workspace first
	path, _, err := Create(context.Background(), root, "ENG-DEL", "", 0)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	// Remove it
	if err := Remove(context.Background(), path, root, "", 0); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("workspace directory should be removed")
	}
}

func TestRemove_BeforeRemoveHookRuns(t *testing.T) {
	root := t.TempDir()

	// Create workspace
	path, _, err := Create(context.Background(), root, "ENG-BRM", "", 0)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	// Place marker in root (not in workspace) so it survives the RemoveAll.
	marker := filepath.Join(root, "before_remove_ran")
	// Use bash-friendly path: $OLDPWD trick or compute relative from workspace to root.
	// Simpler: just use the absolute path with forward slashes for bash.
	bashRoot := filepath.ToSlash(root)
	hook := "echo ok > " + bashRoot + "/before_remove_ran"
	if err := Remove(context.Background(), path, root, hook, 10*time.Second); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("before_remove hook did not run: marker file %q does not exist", marker)
	}
}

func TestRemove_BeforeRemoveHookFailure_DoesNotBlock(t *testing.T) {
	root := t.TempDir()

	// Create workspace
	path, _, err := Create(context.Background(), root, "ENG-BRF", "", 0)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	// Remove with failing before_remove hook -- removal should still succeed
	hook := "exit 1"
	if err := Remove(context.Background(), path, root, hook, 10*time.Second); err != nil {
		t.Fatalf("Remove should not fail even when before_remove hook fails: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("workspace directory should be removed even when hook fails")
	}
}

// ---------------------------------------------------------------------------
// RunHook
// ---------------------------------------------------------------------------

func TestRunHook_Success(t *testing.T) {
	dir := t.TempDir()

	// Use relative filename; cmd.Dir is set to dir.
	err := RunHook(context.Background(), "echo ok > hook_marker", dir, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook returned error: %v", err)
	}

	marker := filepath.Join(dir, "hook_marker")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("hook did not create marker file: %v", err)
	}
}

func TestRunHook_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep command timeout test unreliable on Windows")
	}

	dir := t.TempDir()

	err := RunHook(context.Background(), "sleep 30", dir, 500*time.Millisecond)
	if err == nil {
		t.Error("RunHook should return error on timeout")
	}
}

func TestRunHook_NonZeroExit(t *testing.T) {
	dir := t.TempDir()

	err := RunHook(context.Background(), "exit 42", dir, 10*time.Second)
	if err == nil {
		t.Error("RunHook should return error for non-zero exit")
	}
}

// ---------------------------------------------------------------------------
// Path traversal attack
// ---------------------------------------------------------------------------

func TestPathTraversalAttack(t *testing.T) {
	root := t.TempDir()

	// Identifiers that produce traversal paths after SanitizeKey.
	// Note: SanitizeKey keeps dots, so ".." stays as ".." which
	// filepath.Join resolves as parent directory. IsUnderRoot catches this.
	attackIdentifiers := []string{
		"..",
	}

	for _, identifier := range attackIdentifiers {
		t.Run(identifier, func(t *testing.T) {
			_, _, err := Create(context.Background(), root, identifier, "", 0)
			if err == nil {
				t.Errorf("Create should reject path traversal identifier %q", identifier)
			}
		})
	}
}

func TestPathTraversalSanitizedIdentifiersAreSafe(t *testing.T) {
	root := t.TempDir()

	// These identifiers have dangerous characters that get sanitized,
	// resulting in safe directory names (no traversal).
	safeIdentifiers := []string{
		"../../etc/passwd", // sanitized to ".._.._etc_passwd" (literal name)
		"../..",            // sanitized to ".._.." (literal name)
		"../root",          // sanitized to ".._root" (literal name)
	}

	for _, identifier := range safeIdentifiers {
		t.Run(identifier, func(t *testing.T) {
			path, _, err := Create(context.Background(), root, identifier, "", 0)
			if err != nil {
				t.Fatalf("Create should accept sanitized identifier %q: %v", identifier, err)
			}
			// Verify the directory actually exists and is under root
			if err := IsUnderRoot(root, path); err != nil {
				t.Errorf("created path should be under root: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Concurrent create of same workspace
// ---------------------------------------------------------------------------

func TestConcurrentCreateSameWorkspace(t *testing.T) {
	root := t.TempDir()
	const numGoroutines = 10

	var wg sync.WaitGroup
	results := make([]struct {
		path       string
		createdNow bool
		err        error
	}, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx].path, results[idx].createdNow, results[idx].err = Create(
				context.Background(), root, "CONCURRENT", "", 0,
			)
		}(i)
	}
	wg.Wait()

	// Directory should exist and be under root
	expected := filepath.Join(root, "CONCURRENT")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("workspace directory should exist after concurrent creates: %v", err)
	}

	// Count successes
	createdCount := 0
	errorCount := 0
	for _, r := range results {
		if r.err != nil {
			errorCount++
			continue
		}
		if r.path != expected {
			t.Errorf("path = %q, want %q", r.path, expected)
		}
		if r.createdNow {
			createdCount++
		}
	}

	t.Logf("concurrent create: %d/%d reported createdNow=true, %d errors", createdCount, numGoroutines, errorCount)
}
