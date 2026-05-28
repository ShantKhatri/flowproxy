package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shivalaya/flowproxy/internal/alert"
	"github.com/shivalaya/flowproxy/internal/storage"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		slog.Error("POSTGRES_DSN is required")
		os.Exit(1)
	}

	db, err := storage.ConnectPostgres(ctx, dsn, 10)
	if err != nil {
		slog.Error("postgres connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	slog.Info("postgres connected")

	interval := 30 * time.Second
	if v := os.Getenv("ALERT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}

	w := alert.NewWorker(db, os.Getenv("ALERT_SLACK_WEBHOOK"), interval)

	go w.Run(ctx)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	slog.Info("shutting down alert worker")
	cancel()
}
