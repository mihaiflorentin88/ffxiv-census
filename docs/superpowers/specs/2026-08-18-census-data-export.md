# Spec: Census Data Export (CSV/JSON/NDJSON Streaming with Gzip)

## Overview
Bulk data export endpoint and CLI command for extracting census character data without memory exhaustion.

## Features
1. **Repository Stream**: Low-memory row scanning in SQLite using cursor query without buffering whole tables.
2. **HTTP Endpoint (`GET /api/v1/census/export`)**:
   - Formats: `csv`, `json`, `ndjson` (or `jsonl`).
   - Compression: optional Gzip via `Accept-Encoding: gzip` or `?gzip=true`.
   - Filters: `world`, `datacenter`, `region`, `race`, `name`.
3. **CLI Command (`ffxiv-census export`)**:
   - Stream directly to stdout or file via `-o / --output`.
   - Compression via `-z / --gzip`.
   - Formats via `-f / --format`.
4. **UI Integration**:
   - Export buttons on `/ui/characters` table panel for quick one-click download.
