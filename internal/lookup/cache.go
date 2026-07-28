package lookup

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/harshi79/project-17/internal/model"
	"golang.org/x/sync/singleflight"
)

type Store interface {
	Lookup(context.Context, string) (*model.LookupResult, error)
}

type cacheEntry struct {
	result    *model.LookupResult
	expiresAt time.Time
}

// Service provides a bounded, process-local cache. The entire cache is cheaply
// replaced when it reaches its bound or when an import commits. This avoids an
// unbounded cardinality attack without putting a rate limit in front of users.
type Service struct {
	store Store
	ttl   time.Duration
	max   int

	mu         sync.RWMutex
	entries    map[string]cacheEntry
	inflight   singleflight.Group
	hits       atomic.Uint64
	misses     atomic.Uint64
	generation atomic.Uint64
}

func New(store Store, ttl time.Duration, maxEntries int) *Service {
	return &Service{store: store, ttl: ttl, max: maxEntries, entries: make(map[string]cacheEntry)}
}

func (s *Service) Lookup(ctx context.Context, iin string) (*model.LookupResult, error) {
	now := time.Now()
	s.mu.RLock()
	entry, ok := s.entries[iin]
	s.mu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		s.hits.Add(1)
		return entry.result, nil
	}

	s.misses.Add(1)
	generation := s.generation.Load()
	flightKey := iin + ":" + strconv.FormatUint(generation, 10)
	value, err, _ := s.inflight.Do(flightKey, func() (any, error) {
		return s.store.Lookup(ctx, iin)
	})
	if err != nil {
		return nil, err
	}
	result, _ := value.(*model.LookupResult)
	if s.generation.Load() == generation {
		s.mu.Lock()
		if len(s.entries) >= s.max {
			s.entries = make(map[string]cacheEntry, s.max/2)
		}
		s.entries[iin] = cacheEntry{result: result, expiresAt: now.Add(s.ttl)}
		s.mu.Unlock()
	}
	return result, nil
}

func (s *Service) Purge() {
	s.mu.Lock()
	s.generation.Add(1)
	s.entries = make(map[string]cacheEntry)
	s.mu.Unlock()
}

func (s *Service) Metrics() (hits, misses uint64, entries int) {
	s.mu.RLock()
	entries = len(s.entries)
	s.mu.RUnlock()
	return s.hits.Load(), s.misses.Load(), entries
}
