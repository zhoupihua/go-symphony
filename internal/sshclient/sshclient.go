// Package sshclient provides SSH remote execution for workspace operations
// and agent commands. It supports key-based authentication, command execution
// with timeouts, and remote directory management.
package sshclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	knownhostscallback "golang.org/x/crypto/ssh/knownhosts"
)

// Config holds the SSH client configuration.
type Config struct {
	Host         string // Remote host address (e.g. "192.168.1.10")
	Port         int    // SSH port (default 22)
	User         string // SSH username
	KeyPath      string // Path to private key file
	KnownHosts   string // Path to known_hosts file (optional, empty skips verification)
	ConnectTimeout time.Duration // Dial timeout (default 30s)
}

// SSHClient provides SSH operations for remote execution.
type SSHClient struct {
	cfg Config
}

// New creates a new SSHClient with the given configuration.
func New(cfg Config) *SSHClient {
	applyConfigDefaults(&cfg)
	return &SSHClient{cfg: cfg}
}

// Config returns a copy of the client configuration.
func (c *SSHClient) Config() Config {
	return c.cfg
}

// Dial establishes an SSH connection to the remote host.
// The returned *ssh.Client must be closed with Close when no longer needed.
func (c *SSHClient) Dial(ctx context.Context) (*ssh.Client, error) {
	authMethod, err := c.keyAuthMethod()
	if err != nil {
		return nil, fmt.Errorf("ssh: load key auth: %w", err)
	}

	hostKeyCallback, err := c.hostKeyCallback()
	if err != nil {
		return nil, fmt.Errorf("ssh: host key callback: %w", err)
	}

	addr := c.cfg.Host + ":" + strconv.Itoa(c.cfg.Port)
	sshConfig := &ssh.ClientConfig{
		User:            c.cfg.User,
		Auth:            []ssh.AuthMethod{authMethod},
		HostKeyCallback: hostKeyCallback,
		Timeout:         c.cfg.ConnectTimeout,
	}

	slog.Info("ssh: dialing", "addr", addr, "user", c.cfg.User)

	dialErrCh := make(chan error, 1)
	clientCh := make(chan *ssh.Client, 1)

	go func() {
		client, err := ssh.Dial("tcp", addr, sshConfig)
		if err != nil {
			dialErrCh <- err
			return
		}
		clientCh <- client
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("ssh: dial cancelled: %w", ctx.Err())
	case err := <-dialErrCh:
		return nil, fmt.Errorf("ssh: dial %s: %w", addr, err)
	case client := <-clientCh:
		slog.Info("ssh: connected", "addr", addr)
		return client, nil
	}
}

// RunCommand executes a command on the remote host via the given SSH client.
// If dir is non-empty, the command is prefixed with "cd <dir> && ".
// Returns stdout, stderr, exit code, and any error.
func (c *SSHClient) RunCommand(ctx context.Context, client *ssh.Client, cmd, dir string) (stdout, stderr string, exitCode int, err error) {
	session, err := client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("ssh: new session: %w", err)
	}
	defer session.Close()

	var outBuf, errBuf bytes.Buffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	fullCmd := BuildCommand(cmd, dir)

	slog.Debug("ssh: running command", "cmd", fullCmd, "host", c.cfg.Host)

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- session.Run(fullCmd)
	}()

	select {
	case <-ctx.Done():
		// Close the session to unblock the Run call.
		session.Close()
		return outBuf.String(), errBuf.String(), -1, fmt.Errorf("ssh: command cancelled: %w", ctx.Err())
	case runErr := <-doneCh:
		stdout = outBuf.String()
		stderr = errBuf.String()
		exitCode = exitCodeFromError(runErr)
		if exitCode != 0 {
			slog.Warn("ssh: command exited non-zero",
				"cmd", fullCmd,
				"exitCode", exitCode,
				"stderr", stderr,
			)
			return stdout, stderr, exitCode, fmt.Errorf("ssh: command %q exited with code %d: %s", fullCmd, exitCode, firstLine(stderr))
		}
		return stdout, stderr, 0, nil
	}
}

// MkdirAll creates a remote directory and any necessary parents (like mkdir -p).
func (c *SSHClient) MkdirAll(ctx context.Context, client *ssh.Client, dir string) error {
	cmd := fmt.Sprintf("mkdir -p %s", shellQuote(dir))
	_, _, _, err := c.RunCommand(ctx, client, cmd, "")
	if err != nil {
		return fmt.Errorf("ssh: mkdir -p %s: %w", dir, err)
	}
	return nil
}

// RemoveAll removes a remote directory and its contents (like rm -rf).
func (c *SSHClient) RemoveAll(ctx context.Context, client *ssh.Client, dir string) error {
	cmd := fmt.Sprintf("rm -rf %s", shellQuote(dir))
	_, _, _, err := c.RunCommand(ctx, client, cmd, "")
	if err != nil {
		return fmt.Errorf("ssh: rm -rf %s: %w", dir, err)
	}
	return nil
}

// CopyFile copies a local file to the remote host using cat over SSH.
// The remote file is created with the same basename at remoteDir.
func (c *SSHClient) CopyFile(ctx context.Context, client *ssh.Client, localPath, remoteDir string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("ssh: read local file %s: %w", localPath, err)
	}

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: new session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("ssh: stdin pipe: %w", err)
	}

	remotePath := remoteDir + "/" + shellQuote(baseName(localPath))
	cmd := fmt.Sprintf("cat > %s", remotePath)

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- session.Run(cmd)
	}()

	// Write file content to stdin.
	if _, writeErr := stdin.Write(data); writeErr != nil {
		stdin.Close()
		<-doneCh
		return fmt.Errorf("ssh: write to stdin: %w", writeErr)
	}
	stdin.Close()

	select {
	case <-ctx.Done():
		return fmt.Errorf("ssh: copy cancelled: %w", ctx.Err())
	case runErr := <-doneCh:
		if runErr != nil {
			return fmt.Errorf("ssh: copy file to %s: %w", remotePath, runErr)
		}
		return nil
	}
}

// Close closes the SSH client connection.
func (c *SSHClient) Close(client *ssh.Client) {
	if client == nil {
		return
	}
	err := client.Close()
	if err != nil {
		slog.Warn("ssh: error closing client", "error", err)
	}
}

// BuildCommand constructs a full shell command string, optionally
// prefixing with a directory change. If dir is non-empty, the result
// is "cd <dir> && <cmd>"; otherwise it is just cmd.
func BuildCommand(cmd, dir string) string {
	if dir == "" {
		return cmd
	}
	return "cd " + shellQuote(dir) + " && " + cmd
}

// keyAuthMethod loads the private key from the configured path and returns
// an ssh.AuthMethod for public key authentication.
func (c *SSHClient) keyAuthMethod() (ssh.AuthMethod, error) {
	keyData, err := os.ReadFile(c.cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key %s: %w", c.cfg.KeyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parse private key %s: %w", c.cfg.KeyPath, err)
	}

	return ssh.PublicKeys(signer), nil
}

// hostKeyCallback returns the appropriate HostKeyCallback based on config.
// If KnownHosts is empty, it returns InsecureIgnoreHostKey (with a warning log).
// Otherwise, it parses the known_hosts file for strict verification.
func (c *SSHClient) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if c.cfg.KnownHosts == "" {
		slog.Warn("ssh: known_hosts not configured, skipping host key verification")
		return ssh.InsecureIgnoreHostKey(), nil
	}

	if _, err := os.Stat(c.cfg.KnownHosts); err != nil {
		if errorsIsNotExist(err) {
			slog.Warn("ssh: known_hosts file not found, skipping host key verification", "path", c.cfg.KnownHosts)
			return ssh.InsecureIgnoreHostKey(), nil
		}
		return nil, fmt.Errorf("stat known_hosts %s: %w", c.cfg.KnownHosts, err)
	}

	callback, err := knownhosts(c.cfg.KnownHosts)
	if err != nil {
		return nil, fmt.Errorf("parse known_hosts %s: %w", c.cfg.KnownHosts, err)
	}

	return callback, nil
}

// applyConfigDefaults sets reasonable defaults for zero-valued config fields.
func applyConfigDefaults(cfg *Config) {
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 30 * time.Second
	}
}

// exitCodeFromError extracts the exit code from an ssh.ExitError.
// Returns -1 if the error is nil or not an ExitError.
func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *ssh.ExitError
	if isErrorAs(err, &exitErr) {
		return exitErr.ExitStatus()
	}
	return -1
}

// shellQuote wraps a string in single quotes for safe shell interpolation.
// Any embedded single quotes are escaped as '\''.
func shellQuote(s string) string {
	return "'" + replaceSingleQuote(s) + "'"
}

// replaceSingleQuote replaces ' with '\'' in a string.
func replaceSingleQuote(s string) string {
	var buf []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			buf = append(buf, '\'', '\\', '\'', '\'')
		} else {
			buf = append(buf, s[i])
		}
	}
	return string(buf)
}

// baseName returns the last element of a file path.
func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

// firstLine returns the first non-empty line of s, or s trimmed if no newline.
func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// errorsIsNotExist reports whether err is a "file does not exist" error.
func errorsIsNotExist(err error) bool {
	return err != nil && fs.ErrNotExist.Error() == err.Error()
}

// knownhosts is a variable that holds the knownhosts.New function.
// It can be overridden in tests.
var knownhosts = knownhostscallback.New

// isErrorAs wraps errors.As for testability.
var isErrorAs = errors.As
