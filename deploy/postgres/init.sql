-- FlowProxy schema
CREATE TABLE routes (
    id          SERIAL PRIMARY KEY,
    path_prefix VARCHAR(255) NOT NULL UNIQUE,
    upstream    VARCHAR(512) NOT NULL,
    rate_limit  INTEGER NOT NULL DEFAULT 100,
    window_sec  INTEGER NOT NULL DEFAULT 60,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE clients (
    id                  SERIAL PRIMARY KEY,
    client_id           VARCHAR(255) NOT NULL UNIQUE,
    name                VARCHAR(255),
    rate_limit_override INTEGER,
    is_blocked          BOOLEAN NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE request_logs (
    id          BIGSERIAL PRIMARY KEY,
    client_id   VARCHAR(255),
    route       VARCHAR(255),
    method      VARCHAR(10),
    status_code INTEGER,
    latency_ms  INTEGER,
    ts          TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_request_logs_ts ON request_logs (ts);
CREATE INDEX idx_request_logs_client ON request_logs (client_id, ts);

-- Seed data
INSERT INTO routes (path_prefix, upstream, rate_limit, window_sec) VALUES
    ('/api/', 'http://upstream:80', 100, 60),
    ('/public/', 'http://upstream:80', 1000, 60),
    ('/', 'http://upstream:80', 50, 60);
