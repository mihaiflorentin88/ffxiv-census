# Fix Low Queue Claim Throughput

**Date:** 2026-08-19
**Status:** Implemented

## Problem

With 30 consumer goroutines and 3004 pending `id-sweep` jobs, only ~11 jobs are claimed at any time. The queue is growing, not shrinking: publishing exceeds consumption (~50 jobs/min in, ~2 jobs/min out).

## Root Causes

1. **Claim query missing `FOR UPDATE SKIP LOCKED`** — concurrent goroutines contend on the same rows, causing double-claims and wasted poll cycles.
2. **Tomestone rate too high (10 req/s)** — triggers 429 storms that halve the effective rate and pause all Tomestone consumption globally for 30s.
3. **Chunk-size 1** — each job processes 1 character, requiring 3000 claim/complete DB transactions per hourly batch.

## Changes

### 1. Add `FOR UPDATE SKIP LOCKED` to claim subquery

**File:** `infrastructure/queue/queue.go` — `ClaimMultiple` function.

Added `FOR UPDATE SKIP LOCKED` to the `SELECT id` subquery inside the `UPDATE ... WHERE id IN (...)` statement. Each concurrent transaction now skips rows already locked by others, allowing all 30 goroutines to claim unique jobs in a single cycle.

### 2. Lower Tomestone rate from 10 to 5 req/s

**Files:**
- `config/config.toml` — `rate_limit = 10.0` → `rate_limit = 5.0`
- `README.md` — `TOMESTONE_RATE_LIMIT` default updated from `10.0` to `5.0`

Conservative reduction. If 429s persist, lower to 3 in a follow-up.

### 3. Increase chunk-size from 1 to 10

**File:** `k8s/values.yaml` — both `publish-id-sweep` and `publish-character` cronjobs.

Changed `--chunk-size 1` → `--chunk-size 10`. Reduces claim/complete DB transactions 10x while maintaining acceptable retry granularity (10 chars per retry instead of 1).

## Verification

1. `go test -race ./infrastructure/queue/...` — claim tests pass.
2. `go test -race ./...` — full suite passes.
3. `make lint && make fmt` — clean.
4. `make build` — binary compiles.
5. Deploy: claimed count should be ~28-30 (up from 11).
6. Monitor: no or rare 429 warnings in consumer logs.
