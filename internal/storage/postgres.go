package storage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ConnectPostgres(ctx context.Context, dsn string, maxAttempts int) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	config.MaxConns = 25
	config.MinConns = 5
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = time.Minute

	for i := 0; i < maxAttempts; i++ {
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err == nil {
			if pool.Ping(ctx) == nil {
				return pool, nil
			}
			pool.Close()
		}
		wait := time.Duration(math.Min(math.Pow(2, float64(i)), 32)) * time.Second
		slog.Warn("postgres not ready, retrying", "attempt", i+1, "wait", wait)
		time.Sleep(wait)
	}
	return nil, fmt.Errorf("postgres: failed after %d attempts", maxAttempts)
}
