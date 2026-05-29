package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shivalaya/flowproxy/internal/config"
	"github.com/shivalaya/flowproxy/internal/middleware"
	"github.com/shivalaya/flowproxy/internal/proxy"
	"github.com/shivalaya/flowproxy/internal/ratelimit"
	"github.com/shivalaya/flowproxy/internal/storage"
)

func main() {
	upstream := env("UPSTREAM_URL", "http://httpbin.org")
	addr := env("LISTEN_ADDR", ":8080")

	target, err := url.Parse(upstream)
	if err != nil {
		slog.Error("invalid UPSTREAM_URL", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Postgres
	var logWriter *storage.LogWriter
	var cfgStore *config.Store

	if dsn := os.Getenv("POSTGRES_DSN"); dsn != "" {
		// Redact password in log output.
		redacted := dsn
		if i := strings.Index(dsn, "://"); i > 0 {
			if at := strings.LastIndex(dsn[:strings.Index(dsn[i+3:], "/")+i+3], "@"); at > 0 {
				redacted = dsn[:i+3] + "***@" + dsn[at+1:]
			}
		}
		slog.Info("connecting to postgres", "dsn", redacted)

		db, err := storage.ConnectPostgres(ctx, dsn, 10)
		if err != nil {
			slog.Error("postgres connection failed", "error", err)
			os.Exit(1)
		}
		defer db.Close()
		slog.Info("postgres connected")

		// Config store with hot-reload.
		cfgStore = config.NewStore(envInt("DEFAULT_RATE_LIMIT", 100), envInt("DEFAULT_WINDOW_SEC", 60))
		cfgStore.StartReloader(ctx, db, envDur("CONFIG_RELOAD_INTERVAL", 30*time.Second))

		// Async log writer.
		logWriter = storage.NewLogWriter(db,
			envInt("LOG_BUFFER_SIZE", 10000),
			envInt("LOG_BATCH_SIZE", 100),
			envDur("LOG_FLUSH_INTERVAL", 500*time.Millisecond),
		)
		logWriter.Start()
	}

	// Redis
	var rl *ratelimit.RateLimiter

	if redisAddr := os.Getenv("REDIS_ADDR"); redisAddr != "" {
		rdb, err := storage.ConnectRedis(ctx, redisAddr, os.Getenv("REDIS_PASSWORD"), 10)
		if err != nil {
			slog.Error("redis connection failed", "error", err)
			os.Exit(1)
		}
		defer rdb.Close()
		slog.Info("redis connected")

		rl = ratelimit.New(rdb, env("REDIS_FAILURE_MODE", "fail_open"))
		if err := rl.Warmup(ctx); err != nil {
			slog.Warn("lua script warmup failed", "error", err)
		}
	}

	// Routes
	rp := proxy.New(target)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", rp)

	// Middleware chain: logger to ratelimit to metrics to proxy
	var handler http.Handler = mux

	handler = middleware.Metrics(handler)

	if rl != nil {
		limitFn := func(path string) (int, int) {
			if cfgStore != nil {
				rc := cfgStore.Match(path)
				return rc.RateLimit, rc.WindowSec
			}
			return envInt("DEFAULT_RATE_LIMIT", 100), envInt("DEFAULT_WINDOW_SEC", 60)
		}
		handler = middleware.RateLimit(rl, limitFn)(handler)
	}

	handler = middleware.Logger(logWriter)(handler)

	// Start server
	srv := &http.Server{Addr: addr, Handler: handler}

	go func() {
		slog.Info("flowproxy starting", "addr", addr, "upstream", upstream)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	slog.Info("received shutdown signal, draining in-flight requests")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(),
		envDur("SHUTDOWN_TIMEOUT", 30*time.Second))
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("HTTP server stopped, flushing log buffer")

	cancel() // stop config reloader

	if logWriter != nil {
		logWriter.Drain()
	}
	slog.Info("shutdown complete")
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDur(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
