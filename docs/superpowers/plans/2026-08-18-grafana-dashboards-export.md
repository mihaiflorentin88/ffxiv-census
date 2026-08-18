# Implementation Plan: Grafana Dashboards Export for FFXIV Census

This plan creates four production-grade Grafana dashboards exported as JSON in the repository and imports them directly into the cluster's Grafana instance.

## Context
- **Cluster Datasources Discovered**:
  - Prometheus: Name `Prometheus`, Type `prometheus`, UID `prometheus`
  - Graphite (StatsD): Name `graphite`, Type `graphite`, UID `ef0xmze65xwjka`
- **Dashboards to create**:
  1. `dashboards/ffxiv-census-overview.json`: Platform Executive & Health Overview
  2. `dashboards/ffxiv-census-webapp.json`: Web UI, HTMX Partials & Autoscaling
  3. `dashboards/ffxiv-census-api.json`: REST API Endpoints, Latency Histograms & Errors
  4. `dashboards/ffxiv-census-workers.json`: Queue Workers, Ingest Engine, Providers & CronJobs
  5. `dashboards/README.md`: Documentation on dashboard import, datasources, and panel metrics
- **Direct Grafana API Import**: Post each dashboard JSON to `http://monitoring-grafana.monitoring.svc.cluster.local/api/dashboards/db` so they are immediately live and interactive in the cluster.

## Proposed Changes

### 1. Dashboard JSON Exports (`dashboards/`)
- `dashboards/ffxiv-census-overview.json`
- `dashboards/ffxiv-census-webapp.json`
- `dashboards/ffxiv-census-api.json`
- `dashboards/ffxiv-census-workers.json`
- `dashboards/README.md`

### 2. Documentation Updates
- Update `docs/metrics.md` to reference the dashboards directory and explain Prometheus + StatsD metrics visualization.

### 3. Cluster Verification & Grafana API Import
- POST all dashboard JSON payloads to Grafana `/api/dashboards/db`.
- Query Grafana `/api/search` to confirm all 4 dashboards are imported and healthy.
- Run `make test`, `make lint`, stage, commit, and push.

## Verification Plan
1. Validate all dashboard JSON files with `jq .` to ensure valid Grafana JSON schema.
2. Verify Grafana API import succeeds with status `success` and returns dashboard URLs.
3. Verify metrics queries in Grafana panels match active Prometheus metric names and Graphite paths.
