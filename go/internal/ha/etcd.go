package ha

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// Compile-time interface check.
var _ Elector = (*EtcdElector)(nil)

// EtcdConfig holds configuration for etcd-based leader election.
type EtcdConfig struct {
	// Endpoints is the list of etcd cluster endpoints.
	Endpoints []string

	// LeaseTTL is the lease time-to-live in seconds.
	// If zero, defaults to 10.
	LeaseTTL int

	// AdvertiseAddr is the address this instance advertises as leader.
	// Other instances use this to connect to the leader's dashboard.
	AdvertiseAddr string

	// ElectionKey is the etcd key prefix used for the election.
	// If empty, defaults to "/symphony/leader".
	ElectionKey string
}

// EtcdElector uses etcd's concurrency package for leader election.
type EtcdElector struct {
	cfg    EtcdConfig
	client *clientv3.Client

	mu       sync.RWMutex
	session  *concurrency.Session
	election *concurrency.Election
	leader   bool
	done     chan struct{}
	closed   bool
}

// NewEtcdElector creates an EtcdElector connected to the given etcd cluster.
// The caller must call Close to release resources.
func NewEtcdElector(cfg EtcdConfig) (*EtcdElector, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("ha: etcd endpoints required")
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 10
	}
	if cfg.ElectionKey == "" {
		cfg.ElectionKey = "/symphony/leader"
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints: cfg.Endpoints,
	})
	if err != nil {
		return nil, fmt.Errorf("ha: etcd client: %w", err)
	}

	return &EtcdElector{
		cfg:    cfg,
		client: client,
		done:   make(chan struct{}),
	}, nil
}

// Campaign blocks until this instance becomes leader or the context is cancelled.
// It creates an etcd session and campaigns for leadership.
func (e *EtcdElector) Campaign(ctx context.Context) error {
	e.mu.Lock()
	if e.leader {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	session, err := concurrency.NewSession(e.client, concurrency.WithTTL(e.cfg.LeaseTTL))
	if err != nil {
		return fmt.Errorf("ha: etcd session: %w", err)
	}

	election := concurrency.NewElection(session, e.cfg.ElectionKey)

	slog.Info("ha: campaigning for leadership", "addr", e.cfg.AdvertiseAddr)
	if err := election.Campaign(ctx, e.cfg.AdvertiseAddr); err != nil {
		session.Close()
		return fmt.Errorf("ha: campaign: %w", err)
	}

	e.mu.Lock()
	e.session = session
	e.election = election
	e.leader = true
	e.mu.Unlock()

	slog.Info("ha: elected leader", "addr", e.cfg.AdvertiseAddr)

	// Monitor session loss; close done channel when leadership is lost.
	go func() {
		<-session.Done()
		e.mu.Lock()
		e.leader = false
		if !e.closed {
			close(e.done)
			e.closed = true
		}
		e.mu.Unlock()
		slog.Warn("ha: leadership lost", "addr", e.cfg.AdvertiseAddr)
	}()

	return nil
}

// IsLeader returns true if this instance currently holds leadership.
func (e *EtcdElector) IsLeader() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leader
}

// Resign voluntarily relinquishes leadership and closes the session.
// After resigning, the instance can campaign again.
func (e *EtcdElector) Resign() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.leader || e.election == nil {
		return
	}

	slog.Info("ha: resigning leadership", "addr", e.cfg.AdvertiseAddr)

	ctx, cancel := context.WithTimeout(context.Background(), leaseTTLDuration(e.cfg.LeaseTTL))
	defer cancel()

	if err := e.election.Resign(ctx); err != nil {
		slog.Error("ha: resign failed", "error", err)
	}

	if e.session != nil {
		e.session.Close()
	}

	e.leader = false
	if !e.closed {
		close(e.done)
		e.closed = true
	}
	e.session = nil
	e.election = nil

	// Reset done channel so the elector can be re-campaigned.
	e.done = make(chan struct{})
	e.closed = false
}

// Done returns a channel that is closed when leadership is lost.
func (e *EtcdElector) Done() <-chan struct{} {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.done
}

// LeaderAddr returns the address of the current leader by querying etcd.
func (e *EtcdElector) LeaderAddr() string {
	e.mu.RLock()
	election := e.election
	e.mu.RUnlock()

	if election == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), leaseTTLDuration(e.cfg.LeaseTTL))
	defer cancel()

	resp, err := election.Leader(ctx)
	if err != nil {
		slog.Error("ha: failed to query leader", "error", err)
		return ""
	}

	if len(resp.Kvs) == 0 {
		return ""
	}

	return string(resp.Kvs[0].Value)
}

// Close releases the etcd client and session resources.
func (e *EtcdElector) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.session != nil {
		e.session.Close()
	}
	if !e.closed {
		close(e.done)
		e.closed = true
	}

	return e.client.Close()
}

// leaseTTLDuration converts lease TTL in seconds to a time.Duration.
func leaseTTLDuration(ttl int) time.Duration {
	return time.Duration(ttl) * time.Second
}
