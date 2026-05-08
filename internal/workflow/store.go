package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zhoupihua/go-symphony/internal/config"
)

// pollInterval is how often the file watcher checks for changes.
const pollInterval = 1 * time.Second

// Store watches a WORKFLOW.md file for changes and provides thread-safe
// access to the parsed configuration and prompt template.
type Store struct {
	path        string
	mu          sync.RWMutex
	config      *config.Schema
	prompt      string
	lastModTime time.Time
	lastSize    int64
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewStore creates a file-watching store. It performs an initial load
// immediately and returns an error if that load fails. A background
// goroutine polls the file every second for changes; it exits when ctx
// is cancelled.
func NewStore(ctx context.Context, path string) (*Store, error) {
	// Initial load.
	cfg, prompt, err := loadAndParse(path)
	if err != nil {
		return nil, fmt.Errorf("initial load of %s: %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	childCtx, cancel := context.WithCancel(ctx)

	s := &Store{
		path:        path,
		config:      cfg,
		prompt:      prompt,
		lastModTime: info.ModTime(),
		lastSize:    info.Size(),
		ctx:         childCtx,
		cancel:      cancel,
	}

	go s.watch()

	return s, nil
}

// Current returns the cached config and prompt. Returns an error if no
// successful load has occurred yet.
func (s *Store) Current() (*config.Schema, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, "", fmt.Errorf("workflow not loaded yet")
	}
	return s.config, s.prompt, nil
}

// ForceReload triggers an immediate re-read of the workflow file. If the
// reload fails, it returns the error but retains the last-known-good values.
func (s *Store) ForceReload() error {
	cfg, prompt, err := loadAndParse(s.path)
	if err != nil {
		return fmt.Errorf("force reload %s: %w", s.path, err)
	}

	info, err := os.Stat(s.path)
	if err != nil {
		return fmt.Errorf("stat %s after reload: %w", s.path, err)
	}

	s.mu.Lock()
	s.config = cfg
	s.prompt = prompt
	s.lastModTime = info.ModTime()
	s.lastSize = info.Size()
	s.mu.Unlock()

	return nil
}

// Close stops the watcher goroutine. It is safe to call multiple times.
func (s *Store) Close() {
	s.cancel()
}

// watch polls the file for changes until the context is cancelled.
func (s *Store) watch() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkAndReload()
		}
	}
}

// checkAndReload stats the file and reloads if mtime or size changed.
func (s *Store) checkAndReload() {
	info, err := os.Stat(s.path)
	if err != nil {
		// File might be temporarily deleted during editor save.
		slog.Warn("workflow file stat failed, keeping last-known-good", "path", s.path, "error", err)
		return
	}

	s.mu.RLock()
	modTime := s.lastModTime
	size := s.lastSize
	s.mu.RUnlock()

	if info.ModTime().Equal(modTime) && info.Size() == size {
		return
	}

	cfg, prompt, err := loadAndParse(s.path)
	if err != nil {
		slog.Warn("workflow file reload failed, keeping last-known-good", "path", s.path, "error", err)
		return
	}

	s.mu.Lock()
	s.config = cfg
	s.prompt = prompt
	s.lastModTime = info.ModTime()
	s.lastSize = info.Size()
	s.mu.Unlock()

	slog.Info("workflow file reloaded", "path", s.path)
}

// loadAndParse reads the workflow file and parses it into a typed Schema.
func loadAndParse(path string) (*config.Schema, string, error) {
	raw, prompt, err := Load(path)
	if err != nil {
		return nil, "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	cfg, err := config.Parse(raw, filepath.Dir(absPath))
	if err != nil {
		return nil, "", err
	}
	return cfg, prompt, nil
}
