# FlowProxy: Client Deployment & Integration Guide

This guide outlines exactly how to deploy FlowProxy into a client's existing infrastructure, whether they are running a simple monolithic application on a VPS or a complex microservice architecture on Kubernetes.

As a consultant, your job is to seamlessly drop FlowProxy in front of their traffic without disrupting their existing applications.

---

## Phase 1: Architecture Assessment

Before writing any deployment manifests, determine how the client currently hosts their application:

1. **The Startup/Agency Model (Docker Compose / Single VM)**
   - They have a single large server running multiple containers.
   - **Strategy:** Deploy FlowProxy on the same machine. Update DNS to point to FlowProxy, and route traffic to the internal Docker network.

2. **The Enterprise Model (Kubernetes / Managed Cloud)**
   - They use EKS/GKE, have dozens of microservices, and currently use an AWS Application Load Balancer (ALB) or Nginx Ingress.
   - **Strategy:** Deploy FlowProxy as the "Edge API Gateway" or an Ingress Controller replacement inside their cluster.

---

## Phase 2: Deploying to Kubernetes (Microservices)

When a client has multiple microservices (e.g., `user-service`, `payment-service`), FlowProxy acts as the central router and protector.

### 1. The Traffic Flow
Instead of exposing every microservice to the internet, you expose *only* FlowProxy.
`Internet -> Cloud Load Balancer (ALB) -> FlowProxy -> Internal Microservices`

### 2. Externalizing State (Crucial for K8s)
In Kubernetes, pods are ephemeral. You **cannot** run Postgres and Redis inside the FlowProxy pod.
* **Redis:** Connect FlowProxy to the client's managed Redis (e.g., AWS ElastiCache). This allows you to run 5+ replicas of FlowProxy that all share the exact same rate-limiting state.
* **Postgres:** Connect FlowProxy to a managed Postgres instance (e.g., AWS RDS) so configuration and logs are safely stored outside the cluster.

### 3. Kubernetes Deployment Manifests (Example)
You will write standard K8s YAMLs for the client. 

**Deployment (`flowproxy-deployment.yaml`):**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: flowproxy
spec:
  replicas: 3 # Scale easily!
  selector:
    matchLabels:
      app: flowproxy
  template:
    metadata:
      labels:
        app: flowproxy
    spec:
      containers:
      - name: proxy
        image: shantkhatri/flowproxy:latest
        ports:
        - containerPort: 8080
        env:
        # Instead of an external URL, point to the internal K8s DNS!
        - name: UPSTREAM_URL
          value: "http://internal-ingress-nginx.default.svc.cluster.local" 
        - name: REDIS_ADDR
          value: "prod-redis.client.local:6379"
        - name: POSTGRES_DSN
          valueFrom:
            secretKeyRef:
              name: db-secrets
              key: dsn
```

**Service (`flowproxy-svc.yaml`):**
```yaml
apiVersion: v1
kind: Service
metadata:
  name: flowproxy-service
spec:
  type: LoadBalancer # Exposes FlowProxy to the internet
  ports:
  - port: 80
    targetPort: 8080
  selector:
    app: flowproxy
```

### 4. Routing to Multiple Microservices
FlowProxy currently uses a single `UPSTREAM_URL`. If the client has 20 microservices, you have two options:
1. **The "Gateway" Approach:** Point FlowProxy's `UPSTREAM_URL` to an internal Nginx or K8s Service that handles the sub-routing. FlowProxy handles the rate-limiting and logging, then passes the scrubbed traffic to the internal router.
2. **The "Custom Dev" Upsell:** Tell the client: *"I will modify FlowProxy's Go code so the `routes` table in Postgres directly maps `path_prefix` to different upstream URLs."* (This is a great freelance upsell).

---

## Phase 3: Deploying for Docker Compose (Agencies / SMBs)

If the client just has a few Docker containers on a DigitalOcean droplet:

1. Copy your existing `docker-compose.yml`.
2. Connect FlowProxy to the client's existing Docker network.
3. Change the `UPSTREAM_URL` in `.env` to the internal container name of their app (e.g., `UPSTREAM_URL=http://their-app-container:3000`).
4. Run `docker compose up -d`. FlowProxy now owns port `80` or `443` on their server.

---

## Phase 4: Integrating Observability

Enterprise clients already have observability stacks. They won't want to use a standalone Grafana container.

### 1. Prometheus / Datadog Integration
FlowProxy exposes standard Prometheus metrics at `:8080/metrics`.
* **If they use Prometheus in Kubernetes:** Add Prometheus annotations to the FlowProxy deployment so their cluster automatically scrapes your proxy:
  ```yaml
  metadata:
    annotations:
      prometheus.io/scrape: "true"
      prometheus.io/port: "8080"
      prometheus.io/path: "/metrics"
  ```
* **If they use Datadog:** The Datadog agent can be configured with an OpenMetrics integration to scrape FlowProxy's `/metrics` endpoint natively.

### 2. Exporting the Dashboard
Export your "Blood Red / Critical" Grafana dashboard as a JSON file. Import this file directly into the client's central Grafana instance.

---

## Phase 5: The Client Handover

When you finish the freelance gig, provide the client with:
1. **Admin Guide:** How to update the `routes` table in Postgres to adjust rate limits dynamically.
2. **Alerting Handoff:** Ensure the `ALERT_SLACK_WEBHOOK` is pointing to their engineering team's Slack channel, not yours.
3. **Scaling Instructions:** Explain that because Redis handles the sliding window, they can increase the Kubernetes replicas from 3 to 30 during a Black Friday spike, and the rate limiting will remain perfectly accurate across all nodes.
