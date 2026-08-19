# Retry on Tomestone 404 (Lodestone Sole Source of Truth) Implementation Plan

## Problem Statement
The Lodestone is the authoritative source of truth for all Final Fantasy XIV character data. Tomestone.gg is a third-party indexing service that only possesses a subset of all character records.

Previously, if The Lodestone encountered a transient error (such as a 429 rate limit or scrape timeout) or was paused in the rate limiter, falling back to Tomestone and receiving a `404 Not Found` would erroneously treat the character as non-existent (in `id-sweep`, skipping the ID; in `character-census`, marking the character deleted). This caused active characters not yet indexed by Tomestone to be skipped or falsely deleted during Lodestone cooldowns.

## Solution & Architecture
1. **Lodestone 404 is Authoritative**:
   - A `404 Not Found` directly from The Lodestone confirms that an ID is truly deleted/unallocated.
   - In `id-sweep`, an authoritative Lodestone 404 skips the ID without retrying the chunk.
   - In `character-census`, an authoritative Lodestone 404 marks the character as deleted.
2. **Tomestone 404 Retries on Lodestone**:
   - When Lodestone is unavailable (paused / rate-limited) or fails with an error (429 / 503 / timeout), Tomestone is probed as an opportunistic cache.
   - If Tomestone returns `200 OK`, the character is ingested immediately.
   - If Tomestone returns `404 Not Found`, the handler returns an error (`not found on tomestone, retrying on lodestone`), causing the queue worker to call `queue.Retry` with exponential backoff.
   - Once Lodestone becomes available, the job is retried against The Lodestone to guarantee no character data is lost.

## Changes Made
- `domain/census/handler/character.go`:
  - On Lodestone error + Tomestone 404: returns error to retry on Lodestone (does not mark deleted).
  - On Lodestone paused + Tomestone 404: returns error to retry on Lodestone.
- `domain/census/handler/idsweep.go`:
  - On Lodestone error + Tomestone 404: returns error to retry chunk on Lodestone (does not skip ID).
  - On Lodestone paused + Tomestone 404: returns error to retry chunk on Lodestone.
- Tests updated & verified in `domain/census/handler/character_test.go` and `domain/census/handler/idsweep_test.go`.
