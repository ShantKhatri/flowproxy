# FlowProxy

Self-hosted reverse proxy and API gateway with per-client rate limiting, real-time analytics, and anomaly alerting.

## What It Does

FlowProxy sits in front of any HTTP service and enforces traffic policies without touching your application code:

- **Per-client, per-route rate limiting** - Redis sliding window (sorted sets + atomic Lua script)
- **Dynamic config** - rate limits stored in Postgres, hot-reloaded every 30s without restart
- **Async request logging** - buffered channel → batch insert to Postgres (zero latency impact)
- **Anomaly detection** - background worker fires Slack alerts on traffic spikes, error surges, abuse
- **Prometheus metrics** - `/metrics` endpoint with 7 metrics, pre-built Grafana dashboard
- **Graceful shutdown** - drains in-flight requests, flushes log buffer, exits cleanly on SIGTERM

## Quick Start

### Prerequisites

- Docker and Docker Compose
- (Optional) Go 1.25+ for local development
- (Optional) [hey](https://github.com/rakyll/hey) for load testing: `go install github.com/rakyll/hey@latest`

### Run the Full Stack

```bash
# 1. Clone
git clone https://github.com/ShantKhatri/flowproxy.git
cd flowproxy

# 2. Create .env from example
cp .env.example .env
# Edit .env - set POSTGRES_PASSWORD and update POSTGRES_DSN to match

# 3. Start everything
make up

# 4. Verify
curl http://localhost:8080/healthz
# → {"status":"ok"}

curl http://localhost:8080/get
# → proxied response from upstream httpbin
```

### Run Without Docker (Development)

```bash
# Just the proxy binary, no Redis/Postgres - rate limiting and logging disabled
go run ./cmd/proxy

# Test
curl http://localhost:8080/healthz
curl http://localhost:8080/get
```

## Services

| Service | Port | URL | Purpose |
|---------|------|-----|---------|
| **proxy** | 8080 | http://localhost:8080 | Reverse proxy - all traffic goes here |
| **grafana** | 3000 | http://localhost:3000 | Dashboards (anonymous login, no password) |
| **prometheus** | 9090 | http://localhost:9090 | Metrics explorer |
| postgres | - | internal only | Config store, request logs |
| redis | - | internal only | Rate limit counters |
| alert-worker | - | no ports | Anomaly detection → Slack alerts |
| upstream | - | internal only | httpbin test backend |

## Testing

### 1. Health Check

```bash
curl http://localhost:8080/healthz
# {"status":"ok"}
```

### 2. Proxy Forwarding

```bash
curl http://localhost:8080/get
# Returns proxied httpbin response

curl http://localhost:8080/ip
# Returns your IP as seen by the upstream

curl -X POST http://localhost:8080/post -d '{"hello":"world"}'
# POST forwarded to upstream
```

### 3. Rate Limiting

The default seed config allows 50 requests/minute on `/` and 100 requests/minute on `/api/`:

```bash
# Send 200 requests - expect ~50 with status 200, rest with 429
hey -n 200 -c 10 -H "X-Client-ID: testclient" http://localhost:8080/get

# Check rate limit headers on a single request
curl -v -H "X-Client-ID: testclient" http://localhost:8080/get 2>&1 | grep -i x-ratelimit
# X-RateLimit-Limit: 50
# X-RateLimit-Remaining: 49
```

### 4. Config Hot-Reload

```bash
# Current limit on /api/ is 100 req/min. Change it to 10:
docker compose -f deploy/docker-compose.yml --env-file .env exec postgres \
  psql -U flowproxy -c "UPDATE routes SET rate_limit = 10 WHERE path_prefix = '/api/';"

# Wait 30 seconds for reload, then test:
hey -n 50 -c 5 -H "X-Client-ID: testclient" http://localhost:8080/api/test
# ~10 get 200, rest get 429
```

### 5. Graceful Shutdown

```bash
# Start load test in background
hey -n 5000 -c 50 http://localhost:8080/get &

# Stop proxy while load is running
docker compose -f deploy/docker-compose.yml --env-file .env stop proxy

# Check logs - should show clean drain sequence:
docker compose -f deploy/docker-compose.yml --env-file .env logs proxy --tail 5
# "received shutdown signal, draining in-flight requests"
# "HTTP server stopped, flushing log buffer"
# "shutdown complete"
```

### 6. Request Logs

```bash
# Check logged requests in Postgres
docker compose -f deploy/docker-compose.yml --env-file .env exec postgres \
  psql -U flowproxy -c "SELECT client_id, route, method, status_code, latency_ms FROM request_logs ORDER BY ts DESC LIMIT 10;"
```

### 7. Metrics

```bash
# Raw Prometheus metrics
curl -s http://localhost:8080/metrics | grep flowproxy

# Key metrics:
# flowproxy_requests_total        - total requests by method/route/status
# flowproxy_rate_limited_total    - 429 responses by client/route
# flowproxy_upstream_latency_seconds - latency histogram
# flowproxy_active_connections    - current in-flight requests
# flowproxy_dropped_logs_total    - log entries dropped (buffer full)
```

### 8. Grafana Dashboard

Open http://localhost:3000 - no login needed. The **FlowProxy** dashboard shows:

| Panel | What It Shows |
|-------|--------------|
| Requests/sec | Total request throughput |
| Rate Limit Hits/sec | 429 responses over time |
| Active Connections | Current in-flight requests |
| Upstream Latency | p50/p95/p99 response times |
| Error Rate % | 5xx percentage |
| Top Rate-Limited Clients | Clients hitting limits most often |

### 9. Alert Worker

```bash
# Without Slack (alerts go to stdout):
docker compose -f deploy/docker-compose.yml --env-file .env logs alert-worker --tail 20

# Generate a traffic spike to trigger an alert:
hey -n 2000 -c 100 http://localhost:8080/get

# Within 30s, alert-worker logs should show:
# "Traffic spike: 2000 req/min (10min avg: 50)"
```

## Architecture

```
Client → :8080 → [Logger → RateLimit → Metrics → ReverseProxy] → Upstream
                      │          │          │
                   Postgres    Redis    Prometheus → Grafana
                   (logs)   (counters)  (metrics)
                      ↑
                  Alert Worker (polls anomalies → Slack)
```

### Middleware Chain

Every request passes through this chain in order:

1. **Logger** - records method, path, status, latency; sends log entry to async channel
2. **RateLimit** - checks Redis sliding window; returns 429 if over limit; adds rate limit headers
3. **Metrics** - increments Prometheus counters, records latency histogram
4. **ReverseProxy** - forwards to upstream, returns response

### Rate Limiting Algorithm

Uses Redis sorted sets with an atomic Lua script:

1. `ZREMRANGEBYSCORE` - remove entries older than the sliding window
2. `ZCARD` - count remaining entries
3. If under limit: `ZADD` the new request, `PEXPIRE` the key
4. If over limit: return reset time from oldest entry

This is a **true sliding window** - no fixed-window boundary issues.

### Redis Failure Modes

Controlled by `REDIS_FAILURE_MODE` environment variable:

| Mode | Behavior | Use When |
|------|----------|----------|
| `fail_open` | Allow all traffic, log warning | Availability > security |
| `fail_closed` | Return 503 to all requests | Security > availability |

## Configuration

All configuration via environment variables (`.env` file):

| Variable | Default | Description |
|----------|---------|-------------|
| `UPSTREAM_URL` | `http://httpbin.org` | Backend service URL |
| `LISTEN_ADDR` | `:8080` | Proxy listen address |
| `REDIS_ADDR` | - | Redis address (empty = rate limiting disabled) |
| `REDIS_PASSWORD` | - | Redis AUTH password |
| `REDIS_FAILURE_MODE` | `fail_open` | `fail_open` or `fail_closed` |
| `POSTGRES_DSN` | - | Postgres connection string (empty = logging disabled) |
| `DEFAULT_RATE_LIMIT` | `100` | Fallback rate limit if no route matches |
| `DEFAULT_WINDOW_SEC` | `60` | Fallback window in seconds |
| `CONFIG_RELOAD_INTERVAL` | `30s` | How often to reload routes from Postgres |
| `LOG_BUFFER_SIZE` | `10000` | Async log channel buffer size |
| `LOG_BATCH_SIZE` | `100` | Entries per batch insert |
| `LOG_FLUSH_INTERVAL` | `500ms` | Max time between log flushes |
| `SHUTDOWN_TIMEOUT` | `30s` | Grace period for draining requests |
| `ALERT_SLACK_WEBHOOK` | - | Slack webhook URL (empty = log to stdout) |
| `ALERT_INTERVAL` | `30s` | Alert check frequency |

## Database Schema

### routes
```sql
path_prefix   VARCHAR(255) UNIQUE  -- e.g. '/api/', '/public/'
upstream      VARCHAR(512)         -- backend URL
rate_limit    INTEGER DEFAULT 100  -- max requests per window
window_sec    INTEGER DEFAULT 60   -- window size in seconds
is_active     BOOLEAN DEFAULT true -- soft delete
```

### clients
```sql
client_id           VARCHAR(255) UNIQUE  -- from X-Client-ID header
rate_limit_override INTEGER              -- NULL = use route default
is_blocked          BOOLEAN DEFAULT false
```

### request_logs
```sql
client_id    VARCHAR(255)
route        VARCHAR(255)
method       VARCHAR(10)
status_code  INTEGER
latency_ms   INTEGER
ts           TIMESTAMPTZ DEFAULT NOW()
```

## Makefile Commands

```bash
make up         # Start all services
make down       # Stop all services
make logs       # Follow proxy logs
make logs-all   # Follow all service logs
make test       # Run Go tests
make load-test  # 1000 requests, 50 concurrent
make scan       # Trivy CVE scan on proxy image
make seed       # Insert test route into Postgres
```

## Security

- **Non-root containers** - distroless runtime, `USER nonroot:nonroot`
- **Read-only filesystem** - `read_only: true` on all Go services
- **No secrets in images** - all credentials via `.env` (git-ignored)
- **Network isolation** - Postgres and Redis have no published ports
- **Resource limits** - CPU and memory caps on every service
- **CVE scanning** - GitHub Actions runs Trivy on every push
- **DSN redaction** - Postgres password masked in log output
