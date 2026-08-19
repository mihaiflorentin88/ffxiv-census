# Event Cascading, Dual-Provider Fallback, and Rate Limit Coordination Implementation Plan

## Overview
This plan details the implementation of unified downstream event cascading, primary Lodestone with Tomestone.gg fallback, and provider rate-limiting coordination across `ffxiv-census`.

## Core Features & Objectives
1. **Downstream Event Cascading**: Guarantee that consuming `id-sweep` or `character-census` jobs publishes all dependent downstream events (`achievement-census` and `fc-census` when the character is affiliated with a Free Company) for every ingested character record regardless of data provider.
2. **Primary Lodestone with Tomestone.gg Fallback**: Establish The Lodestone as the primary scraping provider and Tomestone.gg as the high-throughput fallback provider for both `id-sweep` and `character-census` in `auto` mode.
3. **Provider Rate Limit Pausing & Worker Switching**: When Lodestone encounters HTTP 429 or rate limits, workers pause Lodestone-exclusive queues (`achievement-census`, `fc-census`) and switch dual-source queues (`id-sweep`, `character-census`) to Tomestone.gg. When Tomestone is rate-limited, dual-source queues switch to Lodestone. If all providers are rate-limited, workers sleep until the earliest cooldown expires.

---

## Technical Specifications

### 1. Unified Event Chaining Helper
- **File**: `domain/census/handler/event.go`
- **Function**: `BuildDependentCharacterJobs(characterID uint32, freeCompanyID string) []contract.QueueJob`
- **Behavior**:
  - Always enqueues `AchievementCensusJob(characterID)`.
  - Appends `FreeCompanyCensusJob(freeCompanyID)` if `freeCompanyID != ""`.
  - Returns the combined slice of downstream `contract.QueueJob`s.

### 2. Dual-Provider Fallback in `CharacterCensus`
- **File**: `domain/census/handler/character.go`
- **Struct & Constructor**:
  ```go
  type CharacterCensus struct {
      lodestone   contract.LodestoneClient
      tomestone   contract.TomestoneClient
      census      *census.Service
      logger      contract.Logger
      rateLimiter contract.ProviderRateLimiter
  }

  func NewCharacterCensus(
      lodestone contract.LodestoneClient,
      tomestone contract.TomestoneClient,
      svc *census.Service,
      logger contract.Logger,
      rateLimiter ...contract.ProviderRateLimiter,
  ) *CharacterCensus
  ```
- **Flow**:
  1. Check Lodestone and Tomestone availability via `rateLimiter.IsAvailable()`.
  2. If Lodestone is available: query Lodestone.
     - On success: upsert and chain downstream jobs via `BuildDependentCharacterJobs`.
     - On 404 (`contract.ErrCharacterNotFound`): probe Tomestone. If found, upsert and chain. If 404 on both, mark deleted via `census.MarkCharacterDeleted()`.
     - On scrape / transient / 429 error: fallback to Tomestone. If found, upsert and chain. If 404, mark deleted. If Tomestone also fails, return error for queue retry.
  3. If Lodestone is paused / rate-limited and Tomestone is available: query Tomestone directly.
  4. If all providers are rate-limited: return error to trigger queue retry with backoff.

### 3. Dual-Provider Fallback in `IDSweep`
- **File**: `domain/census/handler/idsweep.go`
- **Struct & Constructor**:
  ```go
  type IDSweep struct {
      lodestone   contract.LodestoneClient
      tomestone   contract.TomestoneClient
      census      *census.Service
      logger      contract.Logger
      rateLimiter contract.ProviderRateLimiter
  }

  func NewIDSweep(
      lodestone contract.LodestoneClient,
      tomestone contract.TomestoneClient,
      svc *census.Service,
      logger contract.Logger,
      rateLimiter ...contract.ProviderRateLimiter,
  ) *IDSweep
  ```
- **Flow**:
  - `source: "tomestone"`: probe Tomestone only.
  - `source: "lodestone"`: probe Lodestone only.
  - `source: "auto"`: probe Lodestone first. If 404, transient error, or rate-limited, fallback to Tomestone.gg. Chain all discoveries via `BuildDependentCharacterJobs`.

### 4. Container Dependency Wiring
- **File**: `container/domain.go`
- Register `NewCharacterCensus` and `NewIDSweep` with `s.TomestoneClient()` and `s.ProviderRateLimiter()`.

### 5. Worker Rate-Limiting Coordination
- **File**: `domain/census/worker/worker.go`
- Classify `isEventTypeAvailable`:
  - `achievement-census`, `fc-census`: Lodestone-only (`rateLimiter.IsAvailable(ProviderLodestone)`).
  - `character-census`, `id-sweep`: Dual-source (`rateLimiter.IsAvailable(ProviderLodestone) || rateLimiter.IsAvailable(ProviderTomestone)`).
- When all event types are paused: dispatcher sleeps until `rateLimiter.EarliestAvailable()`.

---

## Verification & Test Results
- Unit tests for `BuildDependentCharacterJobs` in `domain/census/handler/event_test.go`.
- Comprehensive dual-provider fallback and rate-limiting unit tests in `domain/census/handler/character_test.go` and `domain/census/handler/idsweep_test.go`.
- Worker rate-limiting and sleep integration tests in `domain/census/worker/rate_limiting_test.go`.
- Full project test suite passed: `go test -count=1 -p 1 -race ./...` and `make test`.
- Static analysis clean: `make lint`.
- Production build succeeded: `make build`.
