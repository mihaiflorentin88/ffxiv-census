# Metrics & Observability

`ffxiv-census` provides dual-stack observability utilizing **Prometheus** (pull-based `/metrics` endpoint via Kubernetes `ServiceMonitor`) and **StatsD / Graphite** (push-based UDP telemetry).

---

## Prometheus Metrics

Prometheus metrics are exposed at `http://<host>:8080/metrics` and scraped automatically by Prometheus Operator via the Helm chart's `ServiceMonitor`.

### Core Metrics Reference

| Metric Name | Type | Description / Labels |
|---|---|---|
| `http_requests_total` | Counter | Total processed HTTP requests (`method`, `path`, `status`). |
| `http_request_duration_seconds` | Histogram | Request latency distribution (`method`, `path`, `le`). |
| `process_uptime_seconds` | Gauge | Total process uptime in seconds. |
| `sqlite_open_connections` | Gauge | Established open SQLite connections in the pool. |
| `sqlite_in_use_connections` | Gauge | SQLite connections currently in use. |
| `sqlite_idle_connections` | Gauge | Idle SQLite connections in the pool. |
| `queue_jobs_depth` | Gauge | Current count of pending/claimed queue jobs. |

---

## StatsD / Graphite Metrics

StatsD integration sends UDP timing metrics to Graphite/StatsD. Configure the endpoint in `config/config.toml` or environment variables (`METRICS_ENDPOINT`):

```toml
[metrics]
endpoint = "graphite.monitoring.svc.cluster.local:8125"
prefix   = "ffxiv-census"
```

### Emitted Telemetry
- `ffxiv-census.http.<route>` (e.g. `ffxiv-census.http.ui_dashboard`, `ffxiv-census.http.api_v1_census_latest`, `ffxiv-census.http.api_v1_queue`): Request duration timing in milliseconds.

---

## Grafana Dashboards

Ready-to-import Grafana dashboard exports are maintained in the [`dashboards/`](../dashboards/) directory:

1. **System Overview**: [`dashboards/ffxiv-census-overview.json`](../dashboards/ffxiv-census-overview.json) (`uid: ffxiv-census-overview`)
2. **Web Application & UI**: [`dashboards/ffxiv-census-webapp.json`](../dashboards/ffxiv-census-webapp.json) (`uid: ffxiv-census-webapp`)
3. **REST APIs & Swagger**: [`dashboards/ffxiv-census-api.json`](../dashboards/ffxiv-census-api.json) (`uid: ffxiv-census-api`)
4. **Workers, Queue & CronJobs**: [`dashboards/ffxiv-census-workers.json`](../dashboards/ffxiv-census-workers.json) (`uid: ffxiv-census-workers`)

See [`dashboards/README.md`](../dashboards/README.md) for detailed import guides.
