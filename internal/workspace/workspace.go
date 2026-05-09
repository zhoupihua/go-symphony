package workspace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Create creates a workspace directory at <root>/<sanitized_key>.
// Returns the workspace path and whether it was newly created.
// If newly created and afterCreateHook is configured, runs it with timeout.
func Create(ctx context.Context, root, identifier, afterCreateHook string, hookTimeout time.Duration) (string, bool, error) {
	// 1. Sanitize identifier
	key := SanitizeKey(identifier)

	// 2. Join root + sanitized key
	workspacePath := filepath.Join(root, key)

	// 3. Ensure root exists for canonicalization
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", false, fmt.Errorf("create root directory: %w", err)
	}

	// Validate workspace stays under root
	if err := IsUnderRoot(root, workspacePath); err != nil {
		return "", false, fmt.Errorf("workspace path escapes root: %w", err)
	}

	// 4. Check if workspace already exists (Stat before MkdirAll)
	info, err := os.Stat(workspacePath)
	if err == nil && info.IsDir() {
		// Already exists
		return workspacePath, false, nil
	}

	// Create the workspace directory
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		return "", false, fmt.Errorf("create workspace directory: %w", err)
	}

	// 5. Run after_create hook if configured
	if afterCreateHook != "" {
		if err := RunHook(ctx, afterCreateHook, workspacePath, hookTimeout); err != nil {
			// Hook failure for after_create aborts: clean up and return error
			_ = os.RemoveAll(workspacePath)
			return "", false, fmt.Errorf("after_create hook failed: %w", err)
		}
	}

	// 6. Return path, createdNow=true
	return workspacePath, true, nil
}

// Remove removes the workspace directory.
// Runs beforeRemoveHook first if configured.
// Hook failure is logged but does not block removal.
func Remove(ctx context.Context, path, root, beforeRemoveHook string, hookTimeout time.Duration) error {
	// 1. Validate path stays under root
	if err := IsUnderRoot(root, path); err != nil {
		return fmt.Errorf("workspace path escapes root: %w", err)
	}

	// 2. Run before_remove hook if configured (failure is logged, not blocking)
	if beforeRemoveHook != "" {
		if err := RunHook(ctx, beforeRemoveHook, path, hookTimeout); err != nil {
			slog.Warn("before_remove hook failed, continuing with removal", "error", err, "path", path)
		}
	}

	// 3. Remove the directory
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove workspace directory: %w", err)
	}

	return nil
}

// RunHook executes a hook script in the workspace directory.
// The script is run via: bash -lc <script>
// Hook failure semantics vary by caller:
//   - after_create and before_run: abort (return error)
//   - after_run and before_remove: log and ignore
func RunHook(ctx context.Context, script, workspacePath string, timeout time.Duration) error {
	slog.Info("hook start", "path", workspacePath, "timeout", timeout)

	// Create context with timeout
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(hookCtx, "bash", "-lc", script)
	cmd.Dir = workspacePath

	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()

	// Log hook output, truncated to 10KB per SPEC §6.2.
	out := output.String()
	if len(out) > 0 {
		slog.Info("hook output", "path", workspacePath, "output", truncateString(out, 10*1024))
	}

	if err != nil {
		if hookCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("hook timed out after %s: %w", timeout, err)
		}
		return fmt.Errorf("hook exited with error: %w", err)
	}

	return nil
}

// truncateString truncates s to maxLen bytes, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
