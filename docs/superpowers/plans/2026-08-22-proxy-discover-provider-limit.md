# Proxy Discover Provider-Limit Optimization

## Context

The `proxy-discover` CronJob must satisfy its publication limit without unnecessarily querying later proxy providers: process providers in configured order, deduplicate each emitted `(protocol, ip, port)` tuple against PostgreSQL, publish only new tuples, stop as soon as the global limit is filled, and consult the next provider only when the current provider exhausts below the remaining quota. The current `publishDiscoveredProxies` implementation in `cmd/cli/proxy.go` already has this sequential streaming behavior through `errDiscoveryLimitReached`; this change will lock the behavior down with explicit provider-invocation/stream-short-circuit coverage rather than replace the bounded-memory callback design. Documentation will describe the exact limit semantic…

## Approach

1. Before other repository edits, copy this approved plan into `docs/superpowers/plans/2026-08-22-proxy-discover-provider-limit.md` so the implementation record follows the repository convention.
2. In `cmd/cli/proxy_test.go`, extend `fakeProvider` with fetch-call and emitted-record counters that are updated by `FetchProxies` without changing its existing error/record behavior. Add focused table-driven coverage around `publishDiscoveredProxies`:
   - With limit `2`, a first provider containing at least three database-new tuples must be invoked once, stop after emitting/publishing the second tuple, and leave the second provider completely uninvoked (`fetchCalls == 0`).
   - With limit `2`, a first provider containing one database-existing tuple and one new tuple must exhaust normally, then the second provider must be invoked and stop after its first new tuple fills the cumulative limit; the existing tuple must not consume quota.
   - Retain the existing `limit == 0` unlimited contract and existing provider/lookup/publish failure expectations.
   These assertions define the requested observable optimization. They are expected to pass against the existing callback/sentinel implementation; do not introduce buffering, a second deduplication mechanism, or a compatibility path merely to force a production diff.
3. Re-read `cmd/cli/proxy.go` before editing. Keep the existing sequential provider loop, `repo.Exists` database deduplication, successful-publication counting, and `errDiscoveryLimitReached` callback short-circuit if the new tests pass. If the focused tests expose a mismatch, make the smallest correction inside `publishDiscoveredProxies`: only call the next provider after the current one returns below the cumulative limit, return immediately when a successful publish reaches `limit`, and keep `limit <= 0` unlimited. Preserve the existing failure policy and log event names/literals (`proxy.discover.fetching`, `.provider_done`, `.limit_reached`, `.lookup_failed`, `.publish_failed`, `.provider_failed`).
4. Update the behavior-defining documentation:
   - In `docs/proxy.md`, change the CronJob description from hourly/`--limit 5000` to the values-backed schedule every two minutes with `--limit 600`. Expand "Discovery streaming" and "Discovery deduplication" to state that providers are invoked sequentially in configured order, existing database tuples do not consume the global limit, reaching the limit aborts the current provider stream and skips all remaining providers, exhausting below the limit advances to the next provider, and `--limit 0` remains unlimited.
   - In `docs/events.md`, update the `proxy discover` callback-publication paragraph with the same sequential-provider and post-deduplication limit semantics while retaining bounded-memory streaming and the documented partial-failure policy.
5. In the already user-modified `k8s/values.yaml`, change only the global `image.pullPolicy` value from `Always` to the exact Kubernetes value `IfNotPresent`. Preserve every other current working-tree change. Because the Helm helpers and all workload templates inherit this global value unless an instance overrides it, do not duplicate pull-policy settings in CronJob, worker, or webserver blocks.

## Critical files & anchors

- `cmd/cli/proxy.go` — `publishDiscoveredProxies`; authoritative sequential fetch/deduplicate/publish/short-circuit flow.
- `cmd/cli/proxy_test.go` — `fakeProvider` and `TestPublishDiscoveredProxies_LimitCountsPublishedAfterDeduplication`; regression proof for provider invocation and early stream termination.
- `docs/proxy.md` — "Cronjob Scheduling", "Discovery streaming", and "Discovery deduplication"; operator-facing behavior and current schedule/limit.
- `docs/events.md` — "proxy discover callback publication"; event-pipeline semantics.
- `k8s/values.yaml` — global `image.pullPolicy` and the `cronjobs.instances[name: proxy-discover]` schedule/limit used as documentation source of truth.

## Verification

Run from `/home/mihai/Workspace/ffxiv-census`:

1. `go test -run 'TestPublishDiscoveredProxies' ./cmd/cli` — all discovery tests pass. The new limit test must observe input of three new tuples in provider 1 with limit `2`, exactly two queue publications, provider 1 stopping after its second emission, and zero fetch calls to provider 2. The cross-provider test must observe an existing database tuple consuming no quota and provider 2 supplying only the remaining publication.
2. `helm template ffxiv-census ./k8s --show-only templates/cronjobs.yaml --show-only templates/workers.yaml --show-only templates/webserver.yaml` — rendered CronJobs, workers, and webserver contain `imagePullPolicy: IfNotPresent`; the rendered `proxy-discover` CronJob still runs `proxy discover --limit 600` on `*/2 * * * *`.
3. Review the two updated documentation passages against the tested behavior and rendered values: they must say `600`, every two minutes, post-PostgreSQL-deduplication counting, stop/skip after filling the limit, and next-provider fallback only while quota remains.

## Assumptions & contingencies

- `IfNotPresent` is the intended Kubernetes spelling of "if not available"; it applies globally because `k8s/templates/_helpers.tpl` resolves workload pull policy from `.Values.image.pullPolicy`.
- The publication limit is global across the entire discovery run, not reset per provider; database-existing tuples do not count toward it.
- Preserve streaming callbacks rather than collecting a provider's full result set: this is the only design consistent with both the requested avoidance of unnecessary data fetching and the repository's bounded-memory provider contract.
