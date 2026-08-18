# Grafana Dashboards for FFXIV Census

This directory contains production-grade Grafana dashboard exports monitoring all layers of the **ffxiv-census** platform.

---

## Dashboards Overview

| Dashboard File | Title | UID | Target Layer & Key Telemetry |
|---|---|---|---|
| [`ffxiv-census-overview.json`](./ffxiv-census-overview.json) | **FFXIV Census — System Overview** | `ffxiv-census-overview` | System KPIs, total HTTP req/s, P95 latency, queue depth, SQLite in-use connections, uptime, status code breakdown, resource usage. |
| [`ffxiv-census-webapp.json`](./ffxiv-census-webapp.json) | **FFXIV Census — Web Application & UI** | `ffxiv-census-webapp` | Web UI pageviews, route volume (`/ui/*`), HTMX partial requests, StatsD mean/P95 latencies, static asset hits, active web pods. |
| [`ffxiv-census-api.json`](./ffxiv-census-api.json) | **FFXIV Census — REST APIs & Swagger** | `ffxiv-census-api` | Endpoint throughput (`/api/v1/*`), latency heatmap, 4xx/5xx error tracking, Swagger docs access (`/docs/*`), StatsD API timers. |
| [`ffxiv-census-workers.json`](./ffxiv-census-workers.json) | **FFXIV Census — Consumers, Workers & CronJobs** | `ffxiv-census-workers` | Multi-queue depths, consumer worker state, worker pod CPU/Memory usage, restarts, SQLite connection pool telemetry. |

---

## Required Datasources

The dashboards utilize both **Prometheus** (scraped via `ServiceMonitor`) and **Graphite / StatsD** (emitted to StatsD UDP exporter on `:8125`):

| Datasource Variable | Expected Type | Cluster Default Name | Cluster Default UID |
|---|---|---|---|
| `${DS_PROMETHEUS}` | `prometheus` | `Prometheus` | `prometheus` |
| `${DS_GRAPHITE}` | `graphite` | `graphite` | `ef0xmze65xwjka` |

---

## Importing into Grafana

### Option A: Via Grafana Web UI
1. Open your Grafana instance (e.g. `http://grafana.local`).
2. Navigate to **Dashboards** → **New** → **Import**.
3. Click **Upload JSON file** and select any of the dashboard `.json` files in this directory.
4. When prompted, select your **Prometheus** and **Graphite** datasources from the dropdowns.
5. Click **Import**.

### Option B: Via Grafana REST API (cURL / Automated)
To import programmatically from inside or outside the Kubernetes cluster:

```bash
# Obtain Grafana credentials from Kubernetes secret (if in-cluster)
GRAFANA_USER=$(kubectl -n monitoring get secret monitoring-grafana -o jsonpath='{.data.admin-user}' | base64 -d)
GRAFANA_PASS=$(kubectl -n monitoring get secret monitoring-grafana -o jsonpath='{.data.admin-password}' | base64 -d)

# Import a dashboard
for dash in dashboards/*.json; do
  echo "Importing $dash..."
  curl -sS -u "${GRAFANA_USER}:${GRAFANA_PASS}" \
    -H "Content-Type: application/json" \
    -X POST \
    http://monitoring-grafana.monitoring.svc.cluster.local/api/dashboards/db \
    -d "{\"dashboard\": $(cat "$dash"), \"overwrite\": true}"
done
```
