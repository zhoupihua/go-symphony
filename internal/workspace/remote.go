package workspace

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zhoupihua/go-symphony/internal/sshclient"
)

// RemoteOps provides workspace operations over SSH for remote worker hosts.
type RemoteOps struct {
	client *sshclient.SSHClient
}

// NewRemoteOps creates a RemoteOps for the given SSH configuration.
func NewRemoteOps(cfg sshclient.Config) *RemoteOps {
	return &RemoteOps{
		client: sshclient.New(cfg),
	}
}

// CreateRemote creates a workspace directory on a remote host.
// Returns the workspace path and whether it was newly created.
func CreateRemote(ctx context.Context, sshCfg sshclient.Config, root, identifier, afterCreateHook string, hookTimeout time.Duration) (string, bool, error) {
	client := sshclient.New(sshCfg)
	sshConn, err := client.Dial(ctx)
	if err != nil {
		return "", false, fmt.Errorf("remote workspace: dial: %w", err)
	}
	defer client.Close(sshConn)

	key := SanitizeKey(identifier)
	workspacePath := root + "/" + key

	// Check if workspace already exists.
	checkCmd := fmt.Sprintf("test -d %s", shellQuoteRemote(workspacePath))
	_, _, exitCode, _ := client.RunCommand(ctx, sshConn, checkCmd, "")
	if exitCode == 0 {
		return workspacePath, false, nil
	}

	// Create the workspace directory.
	if err := client.MkdirAll(ctx, sshConn, workspacePath); err != nil {
		return "", false, fmt.Errorf("remote workspace: mkdir: %w", err)
	}

	// Run after_create hook if configured.
	if afterCreateHook != "" {
		if err := RunHookRemote(ctx, sshCfg, afterCreateHook, workspacePath, hookTimeout); err != nil {
			// Hook failure aborts: clean up.
			_ = client.RemoveAll(ctx, sshConn, workspacePath)
			return "", false, fmt.Errorf("remote after_create hook failed: %w", err)
		}
	}

	return workspacePath, true, nil
}

// RemoveRemote removes a workspace directory on a remote host.
// Runs beforeRemoveHook first if configured; hook failure is logged but does not block.
func RemoveRemote(ctx context.Context, sshCfg sshclient.Config, path, root, beforeRemoveHook string, hookTimeout time.Duration) error {
	client := sshclient.New(sshCfg)
	sshConn, err := client.Dial(ctx)
	if err != nil {
		return fmt.Errorf("remote workspace remove: dial: %w", err)
	}
	defer client.Close(sshConn)

	// Run before_remove hook (failure is logged, not blocking).
	if beforeRemoveHook != "" {
		if err := RunHookRemote(ctx, sshCfg, beforeRemoveHook, path, hookTimeout); err != nil {
			slog.Warn("remote before_remove hook failed, continuing with removal", "error", err, "path", path)
		}
	}

	if err := client.RemoveAll(ctx, sshConn, path); err != nil {
		return fmt.Errorf("remote workspace remove: %w", err)
	}

	return nil
}

// RunHookRemote executes a hook script in a remote workspace directory.
func RunHookRemote(ctx context.Context, sshCfg sshclient.Config, script, workspacePath string, timeout time.Duration) error {
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := sshclient.New(sshCfg)
	sshConn, err := client.Dial(hookCtx)
	if err != nil {
		return fmt.Errorf("remote hook: dial: %w", err)
	}
	defer client.Close(sshConn)

	cmd := "bash -lc " + shellQuoteRemote(script)
	_, stderr, exitCode, err := client.RunCommand(hookCtx, sshConn, cmd, workspacePath)
	if err != nil {
		if hookCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("remote hook timed out after %s: %w", timeout, err)
		}
		return fmt.Errorf("remote hook exited with error (code %d): %s", exitCode, firstLineRemote(stderr))
	}

	return nil
}

// IsRemote returns true if the given worker host is non-empty (i.e., SSH remote).
func IsRemote(workerHost string) bool {
	return workerHost != ""
}

// shellQuoteRemote wraps a string in single quotes for safe shell interpolation.
func shellQuoteRemote(s string) string {
	result := make([]byte, 0, len(s)+2)
	result = append(result, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			result = append(result, '\'', '\\', '\'', '\'')
		} else {
			result = append(result, s[i])
		}
	}
	result = append(result, '\'')
	return string(result)
}

// firstLineRemote returns the first non-empty line of s.
func firstLineRemote(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
