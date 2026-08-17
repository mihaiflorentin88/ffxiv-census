# Design Spec: Queue Purge by Event Type, Queue Overview API, UI Accordion Drilldown Fix, and Activity Verification

## 1. Background & Motivation
In high-throughput crawl scenarios (such as scanning 60M character IDs or scraping achievements and FCs), operators need fine-grained control to:
1. Purge specific queue job event streams (e.g. purging only `id-sweep` or purging `all` jobs in `failed` or `done` states) without wiping unrelated queues.
2. Inspect queue depth, breakdown, and sample messages grouped by canonical event type (`id-sweep`, `character-census`, `achievement-census`, `fc-census`) in the `/api/v1/queue` inspection endpoint.
3. Fix the "View Worlds ▾" accordion drilldown on the Web UI dashboard so that clicking it repeatedly toggles (expands and collapses) smoothly rather than only expanding.
4. Provide direct visibility into character active status (`is_active: bool`) across REST APIs and document character 36795981's activity state.

## 2. Queue Purge by Event Type & All
### Port Contract
`port/contract/queue.go`:
```go
type QueuePurgeFilter struct {
    EventType string        // Event type name, or "" / "all" for all types
    Status    QueueJobStatus // Status, or "" / "all" for all statuses
    OlderThan time.Duration  // Retention duration threshold
}
```
`PurgeJobs(ctx context.Context, filter QueuePurgeFilter) (int64, error)`

### SQLite Implementation
`infrastructure/sqlite/queue.go`:
```sql
DELETE FROM queue_jobs
WHERE (? = '' OR ? = 'all' OR type = ?)
  AND (? = '' OR ? = 'all' OR status = ?)
  AND created_at < ?
```

### CLI Interface
`ffxiv-census queue purge [--event-type <type|all>] [--status <status|all>] [--older-than <duration>]`

## 3. Enhanced `/api/v1/queue` Overview Endpoint
Returns summary metrics plus event-level stats:
```json
{
  "summary": {
    "total": 1500,
    "pending": 1200,
    "claimed": 50,
    "done": 200,
    "failed": 50
  },
  "events": [
    {
      "type": "id-sweep",
      "description": "ID Sweep Ingestion",
      "total": 1000,
      "pending": 800,
      "claimed": 40,
      "done": 140,
      "failed": 20,
      "messages": [...]
    },
    ...
  ]
}
```

## 4. Web UI Accordion Toggle
In `dashboard.html`, the "View Worlds ▾" button toggles drilldown content via client-side JavaScript checking innerHTML and calling HTMX ajax when collapsed, or clearing when expanded.

## 5. Activity Calculation
A character is active if `latest_achievement_at` or `updated_at` is within the active window (30 days). Expose `is_active: bool` in `CharacterListItem` and `CharacterDetail`.
