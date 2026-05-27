package config

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds route configs in memory, protected by RWMutex.
type Store struct {
	mu       sync.RWMutex
	routes   map[string]RouteConfig
	prefixes []string

	defaultLimit     int
	defaultWindowSec int
}

func NewStore(defaultLimit, defaultWindowSec int) *Store {
	return &Store{
		routes:           make(map[string]RouteConfig),
		defaultLimit:     defaultLimit,
		defaultWindowSec: defaultWindowSec,
	}
}

func (s *Store) Match(path string) RouteConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, prefix := range s.prefixes {
		if strings.HasPrefix(path, prefix) {
			return s.routes[prefix]
		}
	}
	return RouteConfig{RateLimit: s.defaultLimit, WindowSec: s.defaultWindowSec}
}

func (s *Store) Update(routes map[string]RouteConfig) {
	prefixes := make([]string, 0, len(routes))
	for p := range routes {
		prefixes = append(prefixes, p)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		return len(prefixes[i]) > len(prefixes[j])
	})

	s.mu.Lock()
	s.routes = routes
	s.prefixes = prefixes
	s.mu.Unlock()
}

func (s *Store) StartReloader(ctx context.Context, db *pgxpool.Pool, interval time.Duration) {
	if routes, err := LoadRoutes(ctx, db); err != nil {
		slog.Error("initial config load failed", "error", err)
	} else {
		s.Update(routes)
		slog.Info("config loaded", "routes", len(routes))
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				routes, err := LoadRoutes(ctx, db)
				if err != nil {
					slog.Error("config reload failed", "error", err)
					continue
				}
				s.Update(routes)
				slog.Info("config reloaded", "routes", len(routes))
			case <-ctx.Done():
				return
			}
		}
	}()
}
