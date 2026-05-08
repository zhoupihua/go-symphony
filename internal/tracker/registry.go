package tracker

import (
	"fmt"
	"sync"
)

// TrackerFactory creates a Tracker from adapter-specific config.
type TrackerFactory func(config map[string]any) (Tracker, error)

var (
	trackerRegistry = map[string]TrackerFactory{}
	trackerMu       sync.RWMutex
)

// RegisterTracker registers a TrackerFactory under the given kind.
// It panics if a factory is already registered for that kind.
func RegisterTracker(kind string, factory TrackerFactory) {
	trackerMu.Lock()
	defer trackerMu.Unlock()

	if _, exists := trackerRegistry[kind]; exists {
		panic(fmt.Sprintf("tracker already registered: %s", kind))
	}
	trackerRegistry[kind] = factory
}

// NewTracker creates a Tracker of the given kind using the provided config.
// Returns an error if the kind is unknown.
func NewTracker(kind string, config map[string]any) (Tracker, error) {
	trackerMu.RLock()
	factory, ok := trackerRegistry[kind]
	trackerMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown tracker kind: %s", kind)
	}
	return factory(config)
}
