package storage

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LogEntry represents a single request log record.
type LogEntry struct {
	ClientID   string
	Route      string
	Method     string
	StatusCode int
	LatencyMs  int64
	Timestamp  time.Time
}

// LogWriter batches log entries from a channel and flushes to Postgres.
type LogWriter struct {
	db            *pgxpool.Pool
	ch            chan LogEntry
	batchSize     int
	flushInterval time.Duration
	wg            sync.WaitGroup

	// DroppedLogs is exported so the metrics middleware can observe it.
	DroppedLogs int64
	dropMu      sync.Mutex
	lastDropLog time.Time
}

func NewLogWriter(db *pgxpool.Pool, bufSize, batchSize int, flushInterval time.Duration) *LogWriter {
	return &LogWriter{
		db:            db,
		ch:            make(chan LogEntry, bufSize),
		batchSize:     batchSize,
		flushInterval: flushInterval,
	}
}

func (lw *LogWriter) Chan() chan<- LogEntry { return lw.ch }

func (lw *LogWriter) Start() {
	lw.wg.Add(1)
	go func() {
		defer lw.wg.Done()
		lw.loop()
	}()
}

func (lw *LogWriter) loop() {
	ticker := time.NewTicker(lw.flushInterval)
	defer ticker.Stop()

	batch := make([]LogEntry, 0, lw.batchSize)

	for {
		select {
		case entry, ok := <-lw.ch:
			if !ok {
				if len(batch) > 0 {
					lw.flush(batch)
				}
				return
			}
			batch = append(batch, entry)
			if len(batch) >= lw.batchSize {
				lw.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				lw.flush(batch)
				batch = batch[:0]
			}
		}
	}
}

func (lw *LogWriter) flush(entries []LogEntry) {
	rows := make([][]any, len(entries))
	for i, e := range entries {
		rows[i] = []any{e.ClientID, e.Route, e.Method, e.StatusCode, e.LatencyMs, e.Timestamp}
	}

	_, err := lw.db.CopyFrom(
		context.Background(),
		pgx.Identifier{"request_logs"},
		[]string{"client_id", "route", "method", "status_code", "latency_ms", "ts"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		slog.Error("log flush failed", "error", err, "count", len(entries))
	}
}

func (lw *LogWriter) RecordDrop() {
	lw.dropMu.Lock()
	lw.DroppedLogs++
	if time.Since(lw.lastDropLog) > time.Minute {
		slog.Warn("log entries dropped (channel full)", "total_dropped", lw.DroppedLogs)
		lw.lastDropLog = time.Now()
	}
	lw.dropMu.Unlock()
}

func (lw *LogWriter) Drain() {
	close(lw.ch)
	lw.wg.Wait()
}
