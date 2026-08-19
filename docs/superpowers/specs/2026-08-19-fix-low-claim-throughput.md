# Fix Low Queue Claim Throughput — Design Spec

**Date:** 2026-08-19
**Status:** Accepted

## Context

The FFXIV census queue processes thousands of character scan jobs via 30 concurrent goroutines. Under load, only ~11 jobs are claimed simultaneously despite 3000+ pending jobs. This causes the queue to grow faster than it drains.

## Decision 1: `FOR UPDATE SKIP LOCKED` in claim query

**Chosen:** PostgreSQL `FOR UPDATE SKIP LOCKED` on the subquery selecting pending jobs.

**Alternatives considered:**
- Advisory locks — more complex, no ordering guarantees.
- Application-level mutex — serializes claims, defeats concurrency.
- `SELECT ... FOR UPDATE` without `SKIP LOCKED` — workers still block on the same first row.

**Rationale:** `FOR UPDATE SKIP LOCKED` is the idiomatic PostgreSQL pattern for concurrent work queues. Each transaction skips rows locked by others, allowing all workers to claim unique rows in a single cycle. Requires PostgreSQL 9.5+ (CloudNativePG runs PG16).

**Tradeoff:** Under very high concurrency with few pending jobs, some workers may find no unlocked rows and get 0 claims. This is correct behavior — it means the queue is drained.

## Decision 2: Tomestone rate 10 → 5 req/s

**Chosen:** Lower the configured rate limit from 10.0 to 5.0 req/s.

**Rationale:** Tomestone.gg returns 429 at 10 req/s. The internal token bucket halves the rate on each 429 (floor 0.5), and each 429 triggers a 30s global pause via `ProviderRateLimiter.Pause`. The effective throughput during 429 recovery is 0.5–2.5 req/s — worse than a steady 5 req/s.

**Tradeoff:** Discovery throughput is halved at peak, but sustained throughput increases because 429 storms are eliminated.

## Decision 3: Chunk-size 1 → 10

**Chosen:** Process 10 characters per job instead of 1.

**Rationale:** Chunk-size 1 creates 3000 jobs/hour for 3000 characters. Each job requires a claim + complete DB transaction pair. With chunk-size 10, only 300 transactions are needed — 10x less DB overhead.

**Tradeoff:** A failed job retries 10 characters instead of 1. Acceptable because:
- The retry mechanism handles partial failures gracefully.
- The DB overhead reduction is significant under high concurrency.
- Characters in the same chunk are nearby IDs, likely similar in processing time.
