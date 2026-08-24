# Fix Achievement Timestamp Parsing & Eliminate Unnecessary Lodestone API Calls

## Context

Two bugs in the achievement census pipeline:

1. **`achieved_at` always stored as `2026-07-28 08:00:00+00`** — The `extractTimestamp()` function in `infrastructure/lodestone/client.go` uses `FindStringSubmatch` with regex `ldst_strftime\((\d+),` which returns the **first** match in the HTML. The Lodestone achievement detail page (`/lodestone/character/{id}/achievement/detail/{id}/`) contains multiple `ldst_strftime` calls — one for the page-level timestamp (navigation/header) and one for the actual achievement earned date. The first match is the page-level timestamp, not the achievement-specific one.

2. **Unnecessary `checkPrivacy` HTTP call** — `FetchAchievements()` always calls `checkPrivacy()` which fetches `/achievement/` (1 HTTP request) even when the individual achievement detail endpoint already handles HTTP 403 gracefully. This wastes a rate-limited API call on every achievement census.

## Approach

### Step 1: Fix `extractTimestamp` to use the correct `ldst_strftime` call

**File:** `infrastructure/lodestone/client.go`

**Problem:** `FindStringSubmatch` returns the first `ldst_strftime` match, which is the page-level timestamp, not the achievement-specific one.

**Fix:** Changed `extractTimestamp` to use `FindAllStringSubmatch` and return the **last** match. The achievement-specific `ldst_strftime` call appears after the page-level one in the HTML.

**Test:** Added `TestExtractTimestamp` in `infrastructure/lodestone/client_test.go` with HTML fixtures containing multiple `ldst_strftime` calls to verify the last match is used.

### Step 2: Remove `checkPrivacy` entirely — detect 403 from achievement checks

**File:** `infrastructure/lodestone/client.go`

**Problem:** `FetchAchievements()` always calls `checkPrivacy()` which makes 1 HTTP request to `/achievement/`. However, `checkSingleAchievement()` already handles HTTP 403 gracefully — it returns `Earned: false` and the sequential dependency loop breaks immediately. The privacy check is a redundant HTTP call.

**Fix (improved over original plan):** Rather than adding a `skipPrivacy` parameter, removed `checkPrivacy()` entirely. Updated `checkSingleAchievement()` to return `contract.ErrAchievementsPrivate` on HTTP 403 instead of silently treating it as "not earned". `FetchAchievements()` catches this error and returns `&contract.AchievementSummary{Private: true}` — same behavior, one fewer HTTP call unconditionally.

**Why better than original plan:** The original plan proposed a `skipPrivacy ...bool` variadic parameter, which would still make the privacy check HTTP call when the DB didn't have prior knowledge. Removing the call entirely saves 1 HTTP request per character unconditionally, with no loss of functionality — the first achievement check detects privacy just as well.

### Step 3: Update handler to remove skip-privacy logic

**File:** `domain/census/handler/achievement.go`

Removed the `skipPrivacy` logic that queried the DB for `achievements_private` before calling `FetchAchievements`. Simplified the request count calculation. The handler now calls `FetchAchievements` directly without any privacy-related parameters.

### Step 4: Clean up unused code

**File:** `infrastructure/lodestone/client.go`

Removed the `checkPrivacy()` function — no longer called by any code path.

## Files Changed

| File | Change |
|---|---|
| `infrastructure/lodestone/client.go` | `extractTimestamp` → `FindAllStringSubmatch` + last match; `checkSingleAchievement` → returns `ErrAchievementsPrivate` on 403; `FetchAchievements` catches it; removed `checkPrivacy()` |
| `infrastructure/lodestone/client_test.go` | Added `TestExtractTimestamp` (3 cases) |
| `domain/census/handler/achievement.go` | Removed `skipPrivacy` logic, simplified request count |
| `domain/census/handler/achievement_test.go` | Removed obsolete skip-privacy tests |
| `port/contract/lodestone.go` | No net change |
| `mock/lodestone/lodestone.go` | No net change |
| `domain/census/handler/logging_test.go` | No net change |
| `domain/census/worker/proxy_worker_test.go` | No net change |

## Verification

1. **Timestamp fix:** `go test -v -run TestExtractTimestamp ./infrastructure/lodestone/` — verify last `ldst_strftime` match is used
2. **Full test suite:** `make test` — verify no regressions (pre-existing failures in `character_test.go`, `idsweep_test.go`, `TestMethodologyHandler` confirmed unrelated)
3. **Lint:** `make lint` — only pre-existing `errcheck` warnings in `cmd/cli/proxy_test.go`

## Assumptions & Contingencies

- **Assumption:** The Lodestone achievement detail page has multiple `ldst_strftime` calls, and the last one is the achievement-specific timestamp. If the HTML structure changes, the regex may need adjustment.
- **Contingency:** If the last `ldst_strftime` match is still incorrect, use a more specific regex targeting the achievement date element (e.g., `entry__achievement__date` class).
- **Assumption:** HTTP 403 on the achievement detail endpoint reliably indicates a private profile. If Lodestone changes its error codes, the detection may need updating.
