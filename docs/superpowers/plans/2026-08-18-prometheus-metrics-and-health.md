# Prometheus Metrics & Health Readiness Exporter Implementation Plan

## Task Breakdown

### Phase 1: Core Metrics Registry (`infrastructure/metrics`)
- Implement `infrastructure/metrics/registry.go` with thread-safe `Registry`, `Counter`, `Gauge`, `Histogram` with labels.
- Implement text serialization in Prometheus format (including `# HELP`, `# TYPE`, formatted labels, summary/buckets).
- Add tests in `infrastructure/metrics/registry_test.go` covering counter increments, gauge sets/inc/dec, histogram observations, and text exposition rendering.

### Phase 2: Container Wiring (`container/infrastructure.go`)
- Add `PrometheusRegistry` to `InfrastructureContainer` and `container.Load.PrometheusRegistry()`.
- Register runtime/system/sqlite/queue metric hooks if available.

### Phase 3: HTTP Middleware & Handlers (`cmd/http/`)
- Update `cmd/http/middleware/metrics.go` to capture status code and latency, updating both StatsD (if set) and Prometheus metrics.
- Implement `/metrics` handler in `cmd/http/app/root/handler/metrics.go` with unit tests in `metrics_test.go`.
- Implement `/health/live` and `/health/ready` in `cmd/http/app/root/handler/check.go` with unit tests in `check_test.go`.
- Wire routes in `cmd/http/router.go` and update OpenAPI/Swagger docs.

### Phase 4: Verification
- Run `go test -v -race ./...`.
- Verify clean compilation with `make build` and lint/fmt checks.
