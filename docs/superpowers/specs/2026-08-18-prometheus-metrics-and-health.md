# Prometheus Metrics & Health Readiness Exporter Specification

## Background & Motivation
In production environments (such as Kubernetes clusters and container orchestrations), services require standardized observability endpoints:
1. Standard Prometheus metrics scraping (`GET /metrics`) in text exposition format (version 0.0.4 / OpenMetrics compatible).
2. Granular liveness probe (`GET /health` or `GET /health/live`) to detect whether the process is alive.
3. Deep readiness probe (`GET /health/ready`) verifying database connectivity, queue readiness, and external client availability before receiving traffic.

## Architectural Design
Following hexagonal principles and avoiding heavy CGO dependencies:
1. `infrastructure/metrics`:
   - Pure-Go thread-safe Prometheus registry supporting Counters, Gauges, and Histograms with label support.
   - Built-in collectors for HTTP request rates/durations, queue stats, SQLite connection pool health, census summaries, and process uptime.
   - Exporter generating valid Prometheus text exposition output.
2. `cmd/http/middleware`:
   - Middleware instrumenting incoming HTTP requests (duration, method, normalized path, status code), recording to Prometheus registry as well as legacy StatsD.
3. `cmd/http/app/root/handler`:
   - `MetricsController` serving `/metrics` with `text/plain; version=0.0.4; charset=utf-8`.
   - `HealthController` updated to support:
     - `GET /health` and `GET /health/live` -> Liveness (HTTP 200, uptime, status ok).
     - `GET /health/ready` -> Readiness (HTTP 200 when DB/queue healthy, HTTP 503 with JSON breakdown when unhealthy).
4. `container`:
   - Expose `container.Load.PrometheusRegistry()` in `ServiceContainer`.
   - Wire dependencies into `NewRouter()` / controllers.
