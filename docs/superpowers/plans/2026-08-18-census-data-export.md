# Implementation Plan: Streaming Census Data Export

Status: Implemented & Verified

## Execution Summary
- **Domain Contract**: Added `Stream` method to `CharacterRepository` contract and mock.
- **SQLite Driver**: Added row-by-row streaming `Stream` method on `CharacterRepository`.
- **Domain Service**: Added `StreamCharacters` to `census.Service`.
- **HTTP Layer**: Added streaming export controller at `GET /api/v1/census/export` with CSV/JSON/NDJSON & Gzip support.
- **CLI Command**: Added `ffxiv-census export` command in `cmd/cli/export.go`.
- **Web UI**: Added CSV/JSON/NDJSON/CSV.GZ export buttons to the character directory table header.
- **API Specs**: Documented export endpoint in OpenAPI/Swagger specs.
