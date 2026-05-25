package storage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

// ConnectRedis creates a Redis client with exponential backoff retry.
func ConnectRedis(ctx context.Context, addr, password string, maxAttempts int) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
	})

	for i := 0; i < maxAttempts; i++ {
		if client.Ping(ctx).Err() == nil {
			return client, nil
		}
		wait := time.Duration(math.Min(math.Pow(2, float64(i)), 32)) * time.Second
		slog.Warn("redis not ready, retrying", "attempt", i+1, "wait", wait)
		time.Sleep(wait)
	}
	client.Close()
	return nil, fmt.Errorf("redis: failed after %d attempts", maxAttempts)
}
