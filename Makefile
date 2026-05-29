COMPOSE = docker compose -f deploy/docker-compose.yml --env-file .env

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f proxy

logs-all:
	$(COMPOSE) logs -f

test:
	go test ./...

load-test:
	hey -n 1000 -c 50 -H "X-Client-ID: testclient" http://localhost:8080/get

scan:
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy image flowproxy:latest

seed:
	$(COMPOSE) exec postgres psql -U flowproxy -c "INSERT INTO routes (path_prefix, upstream, rate_limit, window_sec) VALUES ('/test/', 'http://upstream:80', 10, 60);"
