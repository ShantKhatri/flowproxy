package config

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RouteConfig holds per-route settings loaded from Postgres.
type RouteConfig struct {
	ID         int
	PathPrefix string
	Upstream   string
	RateLimit  int
	WindowSec  int
	IsActive   bool
	UpdatedAt  time.Time
}

// LoadRoutes fetches all active routes from Postgres.
func LoadRoutes(ctx context.Context, db *pgxpool.Pool) (map[string]RouteConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := db.Query(ctx,
		`SELECT id, path_prefix, upstream, rate_limit, window_sec, is_active, updated_at
		 FROM routes WHERE is_active = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	routes := make(map[string]RouteConfig)
	for rows.Next() {
		var rc RouteConfig
		if err := rows.Scan(&rc.ID, &rc.PathPrefix, &rc.Upstream, &rc.RateLimit,
			&rc.WindowSec, &rc.IsActive, &rc.UpdatedAt); err != nil {
			return nil, err
		}
		routes[rc.PathPrefix] = rc
	}
	return routes, rows.Err()
}
