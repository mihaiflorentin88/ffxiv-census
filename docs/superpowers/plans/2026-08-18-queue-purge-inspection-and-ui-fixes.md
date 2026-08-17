# Implementation Plan: Queue Purge, Enhanced Queue API, UI Accordion Fix & Activity Verification

## Context
Add queue event purging by event type (and "all"), enhance `/api/v1/queue` inspection to return grouped messages and counters per event name, fix the "View Worlds ▾" accordion toggle in the Web UI dashboard so it collapses when clicked again, verify character activity calculation for character 36795981, persist all superpowers specs/plans (including uncommitted plans from this session) to `docs/superpowers/`, and ensure subagents use `skill:delegate-data-gathering` followed by `make fmt`, `make lint`, and `git commit & push`.

---

## Approach

### Step 1: Superpowers Documentation Sync
- Write the full feature spec to `docs/superpowers/specs/2026-08-18-queue-purge-inspection-and-ui-fixes.md`.
- Write the remediation plan that was executed to `docs/superpowers/plans/2026-08-18-code-review-and-embed-remediation.md`.
- Write this implementation plan to `docs/superpowers/plans/2026-08-18-queue-purge-inspection-and-ui-fixes.md`.

### Step 2: Queue Purge by Event Type & All (`port/contract`, `infrastructure/sqlite`, `mock/queue`, `cmd/cli`, `cmd/http`)
- **Contract (`port/contract/queue.go`)**:
  - Update or add `PurgeJobs(ctx context.Context, filter QueuePurgeFilter) (int64, error)` or extend `PurgeJobs(ctx context.Context, eventType string, status QueueJobStatus, olderThan time.Duration) (int64, error)` where `eventType == ""` or `"all"` matches all event types, and `status == ""` or `"all"` matches all statuses.
- **SQLite Implementation (`infrastructure/sqlite/queue.go`)**:
  - Update SQL query in `PurgeJobs`:
    `DELETE FROM queue_jobs WHERE (? = '' OR ? = 'all' OR type = ?) AND (? = '' OR ? = 'all' OR status = ?) AND created_at < ?`
- **Mock Implementation (`mock/queue/queue.go`)**:
  - Update mock filter matching logic for event type and status wildcards.
- **CLI Commands (`cmd/cli/queue.go`, `cmd/cli/publish.go`)**:
  - Add `--event-type` / `--type` flag to `queue purge` command with default `"all"` or specific event type.
  - Add `--purge-event` flag to `publish` and `queue purge`.
  - Allow `ffxiv-census queue purge --event-type id-sweep` and `ffxiv-census queue purge --event-type all --status all`.
- **HTTP Controller (`cmd/http/app/census/handler/queue.go`)**:
  - Update `POST /api/v1/queue/purge` to parse `type`/`event_type` and `status` ("all", "done", "failed").

### Step 3: Enhanced `/api/v1/queue` Inspection Endpoint
- **DTOs (`port/dto/response/queue.go`)**:
  - Define `QueueOverviewResponse`:
    - `Summary`: `{ Total, Pending, Claimed, Done, Failed }`
    - `Events`: array of `QueueEventOverview` containing `Type`, `Description`, `Pending`, `Claimed`, `Done`, `Failed`, `Total`, and `Messages` (sampled or paginated `QueueJobItem` list).
- **Controller (`cmd/http/app/census/handler/queue.go`)**:
  - In `QueueController.Depth` (or a dedicated overview handler for `/api/v1/queue`):
    - Query `GetEventDetails` and `Depth`.
    - Group messages and counters per canonical event type (`id-sweep`, `character-census`, `achievement-census`, `fc-census`).
    - Return `QueueOverviewResponse` as JSON.
- **OpenAPI / Swagger (`cmd/http/resource/swagger/`)**:
  - Update `swagger.json` and `docs.go` with the new `/api/v1/queue` schema.

### Step 4: UI Drilldown Accordion Toggle Bugfix
- **Template (`cmd/http/ui/templates/dashboard.html`)**:
  - Update the "View Worlds ▾" button in `dashboard.html`.
  - Add client-side toggle or HTMX conditional swap:
    ```html
    <button 
        class="btn btn-outline btn-drilldown" 
        style="font-size: 0.8rem; padding: 0.3rem 0.75rem;"
        onclick="toggleWorldDrilldown(this, '{{.Region}}')"
    >
        View Worlds ▾
    </button>
    ```
  - In JavaScript:
    ```javascript
    function toggleWorldDrilldown(btn, region) {
        const target = document.getElementById('drilldown-' + region);
        if (target.innerHTML.trim().length > 0) {
            target.innerHTML = '';
            btn.innerHTML = 'View Worlds ▾';
            btn.classList.remove('active');
        } else {
            htmx.ajax('GET', '/ui/partials/world-breakdown?region=' + encodeURIComponent(region), {
                target: target,
                swap: 'innerHTML'
            });
            btn.innerHTML = 'Hide Worlds ▴';
            btn.classList.add('active');
        }
    }
    ```

### Step 5: Character Activity Verification & Transparency
- **Domain & DTO (`port/dto/response/census.go`, `domain/census/service.go`)**:
  - In `CharacterListItem` and `CharacterDetail`, include `is_active: bool` directly so clients querying `/api/v1/census/characters/{id}` can immediately see active status without manual calculation.
  - Explain & verify character 36795981:
    - Running `GET /api/v1/census/characters/36795981` checks `latest_achievement_at`.
    - If `latest_achievement_at` is nil or older than 30 days, character is inactive. Triggering `achievement-census` or `id-sweep` populates `latest_achievement_at` to mark active.

### Step 6: Verification, Formatting, Linting & Git Push
- Run `go test -v -race ./...` across all packages.
- Run `make fmt` and `PATH="$HOME/go/bin:$PATH" make lint`.
- Build binary with `make build`.
- Execute `./bin/ffxiv-census queue purge --help` and verify CLI flags.
- Commit all changes with a descriptive message and `git push origin master`.

---

## Critical Files & Anchors
- `port/contract/queue.go`: Queue interface method signatures for PurgeJobs and Event stats.
- `infrastructure/sqlite/queue.go`: SQLite implementation of PurgeJobs with wildcard matching.
- `cmd/http/app/census/handler/queue.go`: `/api/v1/queue` and `/api/v1/queue/purge` handlers.
- `cmd/cli/queue.go`: CLI commands for `queue purge` with `--event-type` and `all`.
- `cmd/http/ui/templates/dashboard.html`: World drilldown button toggle logic.
- `docs/superpowers/`: Directory where all specs and plans must be saved.

---

## Verification
- Unit test: `TestQueue_PurgeByEventTypeAndAll` testing `PurgeJobs` with `"all"`, `"id-sweep"`, and status filters.
- Unit test: `TestQueueController_OverviewEndpoint` verifying `/api/v1/queue` JSON output with event grouping and counters.
- Browser / Smoke test: Web UI dashboard world toggle expand/collapse behavior.
- Full verification: `go test -v -race ./...`, `make lint`, `make build`.
