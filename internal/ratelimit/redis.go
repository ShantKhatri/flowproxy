package ratelimit

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var luaSource string

var script = redis.NewScript(luaSource)

// RateLimitInfo holds the result of a rate limit check.
type RateLimitInfo struct {
	Remaining int64
	ResetAtMs int64
	Limit     int64
}

// RateLimiter checks per-key rate limits against Redis.
type RateLimiter struct {
	client   *redis.Client
	failMode string
}

func New(client *redis.Client, failMode string) *RateLimiter {
	return &RateLimiter{client: client, failMode: failMode}
}

func (rl *RateLimiter) Warmup(ctx context.Context) error {
	return script.Load(ctx, rl.client).Err()
}

func (rl *RateLimiter) Allow(ctx context.Context, clientID, route string, limit int, windowSec int) (bool, RateLimitInfo, error) {
	key := fmt.Sprintf("ratelimit:%s:%s", clientID, route)
	nowMs := time.Now().UnixMilli()
	windowMs := int64(windowSec) * 1000

	result, err := script.Run(ctx, rl.client, []string{key},
		nowMs, windowMs, limit, uuid.New().String(),
	).Int64Slice()

	// NOSCRIPT: script evicted from cache, reload and retry once.
	if err != nil && strings.Contains(err.Error(), "NOSCRIPT") {
		_ = rl.Warmup(ctx)
		result, err = script.Run(ctx, rl.client, []string{key},
			nowMs, windowMs, limit, uuid.New().String(),
		).Int64Slice()
	}

	if err != nil {
		return rl.handleFailure(err)
	}

	info := RateLimitInfo{Limit: int64(limit)}
	if result[0] == 0 {
		info.Remaining = result[1]
		return true, info, nil
	}
	info.ResetAtMs = result[1]
	info.Remaining = 0
	return false, info, nil
}

func (rl *RateLimiter) handleFailure(err error) (bool, RateLimitInfo, error) {
	if rl.failMode == "fail_open" {
		slog.Warn("redis error, failing open", "error", err)
		return true, RateLimitInfo{}, nil
	}
	return false, RateLimitInfo{}, err
}
