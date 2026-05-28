package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Worker polls Postgres for anomalies and sends alerts.
type Worker struct {
	db          *pgxpool.Pool
	webhookURL  string
	interval    time.Duration
	lastAlerted map[string]time.Time
}

func NewWorker(db *pgxpool.Pool, webhookURL string, interval time.Duration) *Worker {
	return &Worker{
		db:          db,
		webhookURL:  webhookURL,
		interval:    interval,
		lastAlerted: make(map[string]time.Time),
	}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	slog.Info("alert worker started", "interval", w.interval)
	for {
		select {
		case <-ticker.C:
			w.checkTrafficSpike(ctx)
			w.checkErrorRate(ctx)
			w.checkRepeatRateLimiters(ctx)
			w.checkUpstreamDown(ctx)
		case <-ctx.Done():
			slog.Info("alert worker stopped")
			return
		}
	}
}

func (w *Worker) checkTrafficSpike(ctx context.Context) {
	var lastMin, last10Avg float64
	err := w.db.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM request_logs WHERE ts > NOW() - INTERVAL '1 minute'),
			(SELECT COUNT(*) / 10.0 FROM request_logs WHERE ts > NOW() - INTERVAL '10 minutes')
	`).Scan(&lastMin, &last10Avg)
	if err != nil || last10Avg <= 10 {
		return
	}
	if lastMin > last10Avg*3 {
		w.alert("traffic_spike", fmt.Sprintf(
			"🔥 Traffic spike: %.0f req/min (10min avg: %.0f)", lastMin, last10Avg))
	}
}

func (w *Worker) checkErrorRate(ctx context.Context) {
	var total, errors5xx float64
	err := w.db.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status_code >= 500)
		FROM request_logs WHERE ts > NOW() - INTERVAL '5 minutes'
	`).Scan(&total, &errors5xx)
	if err != nil || total <= 50 {
		return
	}
	pct := (errors5xx / total) * 100
	if pct > 10 {
		w.alert("error_rate", fmt.Sprintf(
			"🚨 Error rate %.1f%% in last 5 min (%.0f 5xx / %.0f total)", pct, errors5xx, total))
	}
}

func (w *Worker) checkRepeatRateLimiters(ctx context.Context) {
	rows, err := w.db.Query(ctx, `
		SELECT client_id, COUNT(*) as cnt
		FROM request_logs
		WHERE ts > NOW() - INTERVAL '10 minutes' AND status_code = 429
		GROUP BY client_id HAVING COUNT(*) > 50`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var clientID string
		var count int
		if rows.Scan(&clientID, &count) == nil {
			w.alert("rate_limit_abuse:"+clientID, fmt.Sprintf(
				"Client %s hit rate limit %d times in 10 min — possible abuse", clientID, count))
		}
	}
}

func (w *Worker) checkUpstreamDown(ctx context.Context) {
	var gatewayErrors, successes int
	err := w.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status_code IN (502, 503)),
			COUNT(*) FILTER (WHERE status_code >= 200 AND status_code < 300)
		FROM request_logs WHERE ts > NOW() - INTERVAL '2 minutes'
	`).Scan(&gatewayErrors, &successes)
	if err != nil {
		return
	}
	if gatewayErrors > 10 && successes == 0 {
		w.alert("upstream_down", fmt.Sprintf(
			"Upstream appears unreachable — %d gateway errors in last 2 min, zero successes", gatewayErrors))
	}
}

func (w *Worker) alert(ruleKey, message string) {
	// Dedup: skip if same rule fired within 5 minutes.
	if t, ok := w.lastAlerted[ruleKey]; ok && time.Since(t) < 5*time.Minute {
		return
	}
	w.lastAlerted[ruleKey] = time.Now()

	slog.Warn("ALERT", "rule", ruleKey, "message", message)

	if w.webhookURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"text": message})
	resp, err := http.Post(w.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Error("slack webhook failed", "error", err)
		return
	}
	resp.Body.Close()
}
