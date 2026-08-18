# Design Spec: Grafana Dashboards for FFXIV Census

## Overview
This specification details the structure, datasources, and panel layouts for Grafana dashboards monitoring the **ffxiv-census** platform. It defines exportable JSON dashboards utilizing both **Prometheus** and **Graphite (StatsD)** datasources discovered from the Kubernetes cluster.

## Cluster Datasource Reference
The Kubernetes cluster running Grafana in the `monitoring` namespace provides the following datasources:

| Datasource Name | Type | UID | Description / Endpoint |
|---|---|---|---|
| **Prometheus** | `prometheus` | `prometheus` | `http://monitoring-kube-prometheus-prometheus.monitoring:9090/` (Scrapes `/metrics` via ServiceMonitor) |
| **graphite** | `graphite` | `ef0xmze65xwjka` | `http://graphite.monitoring.svc.cluster.local:8080` (Receives StatsD timers on port 8125 UDP) |

Dashboards will use parameterized datasource variables (`${DS_PROMETHEUS}` and `${DS_GRAPHITE}`) with defaults matching the cluster's UIDs so they work immediately upon import and remain portable across different Grafana installations.

## Dashboard Architecture

### 1. FFXIV Census — System Overview (`dashboards/ffxiv-census-overview.json`)
- **Executive Stat Cards**: Total Characters, Active Ingest Throughput, Total HTTP Requests/sec, P95 Request Latency, Queue Depth, SQLite In-Use Connections, System Uptime.
- **Traffic & Status Distribution**: HTTP Request Rate by Status Code (2xx, 3xx, 4xx, 5xx) and Status Code Ratio.
- **Latency Overview**: HTTP Request Duration Percentiles (P50, P90, P99) across Web & API routes.
- **Worker & Ingest Pipeline**: Active Claims, Ingest Rate by Event Type (`id-sweep`, `character-census`, `achievement-census`, `fc-census`).
- **Resource Utilization**: Web Pod CPU & Memory Usage, Worker Pod Resource Usage.
- **Quick Links**: Navigation links between dashboards.

### 2. FFXIV Census — Web Application & UI (`dashboards/ffxiv-census-webapp.json`)
- **Web Traffic Overview**: Total Pageviews/sec, Active Users / Sessions.
- **Route Breakdown**: Request volume per UI route (`/ui/dashboard`, `/ui/races`, `/ui/worlds`, `/ui/expansions`, `/ui/methodology`).
- **HTMX Partial Requests**: Request rates and latencies for HTMX partial loads (e.g. `/ui/partials/world-breakdown`).
- **StatsD Route Latency**: Detailed route timing distributions from Graphite (`stats.timers.ffxiv-census.http.ui_dashboard.*`, mean, upper_95, upper_99).
- **Frontend Health**: 4xx / 5xx error tracking on web routes, Static Asset caching/hits (`/ui/assets/*`).
- **HPA & Autoscaling**: Web Deployment Replica Count, Target CPU/Memory vs Current Utilization.

### 3. FFXIV Census — REST APIs & Swagger (`dashboards/ffxiv-census-api.json`)
- **API Request Rates**: Breakdown by HTTP method and endpoint (`/api/v1/census/latest`, `/api/v1/census/characters`, `/api/v1/stats/breakdown`, `/api/v1/queue`, `/docs/*`).
- **Response Latency Heatmap**: Latency distribution across all API endpoints using Prometheus histogram buckets (`http_request_duration_seconds_bucket`).
- **Endpoint Performance Table**: Per-endpoint request rate, P50 latency, P95 latency, P99 latency, and error rate.
- **Error Tracking**: Detailed breakdown of 4xx (client errors) and 5xx (server errors) by path and status code.
- **Queue API & Administrative Ops**: Activity on `/api/v1/queue/*` (`retry-failed`, `purge`, `jobs`).

### 4. FFXIV Census — Consumers, Workers & CronJobs (`dashboards/ffxiv-census-workers.json`)
- **Queue State & Depth**: Pending, Claimed, Done, and Failed jobs across all 4 event types.
- **Worker Ingestion Throughput**: Characters discovered/sec, Achievements processed/sec, FCs updated/sec.
- **External Provider Health & Rate Limiting**:
  - Lodestone scraper requests & 429 rate limit events / cooldown pauses.
  - Tomestone API calls, response latencies, and rate limit status.
- **Database & Connection Pool**: SQLite Open Connections, In-Use Connections, Idle Connections, Busy Timeouts / Retries.
- **CronJobs & Publishers**: Execution history, duration, and status for `id-sweep`, `publish-character`, `publish-fc-census`, and `backup` jobs.

## Export & Documentation
- All JSON files placed in `dashboards/`.
- `dashboards/README.md` documenting manual import instructions, datasource requirements, and panel descriptions.
- Direct cluster import via Grafana REST API (`/api/dashboards/db`).
