# SEO Crawlability: Indexable Pages, Sitemap, and Search Presentation

Date: 2026-08-31
Status: implemented — released v1.11.16, 2026-08-31

## Problem Statement

The site is registered in Google Search Console, but two weeks after registration its pages are still not appearing in search results. Nothing blocks crawlers — robots.txt allows everything, pages are server-rendered HTML, and no bot is challenged at the edge — but Google has almost nothing to work with: there is no sitemap, the homepage URL redirects instead of serving content, pages carry no canonical URLs (so filter permutations look like duplicate content), there are no meta descriptions or site-name signals, and there is no favicon to show in results. Roughly ninety real content pages (including one per game world) exist, yet Google can only find them by stumbling through navigation links.

## Solution

The app itself becomes fully crawler-legible: it serves an app-generated `sitemap.xml` listing every indexable page with fresh timestamps tied to the statistics snapshot, the root URL serves the dashboard directly, every page carries a canonical URL and meta description plus site-name/Open-Graph signals, and a favicon is served and linked. The public domain is configurable so generated absolute URLs are correct in every environment. After deployment, the operator submits the sitemap in Search Console and requests re-indexing; Cloudflare's managed robots.txt stays as the crawl-policy surface, and no `noindex` headers are used anywhere.

## User Stories

1. As a search-engine crawler, I want a `sitemap.xml` endpoint listing every indexable page, so that I can discover all content without relying on link crawling.
2. As a search-engine crawler, I want one sitemap entry per game world page, so that world-level census content is discovered and indexed.
3. As a search-engine crawler, I want sitemap `lastmod` values derived from the statistics snapshot generation time, so that I can prioritize re-crawling when census data actually changes.
4. As a search-engine crawler, I want canonical link tags on every page, so that filtered views of the same page consolidate onto one indexing URL instead of fragmenting signals.
5. As a search-engine crawler, I want the sitemap served as valid XML with the correct content type even while statistics are being refreshed, so that crawling never encounters errors during snapshot windows.
6. As a site visitor arriving from search results, I want the root URL to render the census dashboard directly, so that the URL shown in results serves content with no redirect hop.
7. As a site visitor arriving from search results, I want per-world pages to be indexed, so that searching for a world's population statistics leads me to its census page.
8. As a site visitor arriving from search results, I want meta descriptions on every page, so that the result snippet tells me what the page offers before I click.
9. As a site visitor sharing a census page, I want Open Graph title and description tags, so that shared links preview with the page's real title and summary.
10. As a site visitor scanning search results, I want the site to present a favicon, so that results are visually recognizable as FFXIV Census.
11. As a site operator, I want Google to display "FFXIV Census" as the site name via a site-name signal, so that the brand appears correctly in results.
12. As a site operator, I want the sitemap generated from the live snapshot's world set, so that newly added or removed game worlds are reflected without hand-maintaining URL lists.
13. As a site operator, I want the public base URL configurable via the standard configuration mechanism, so that local, staging, and production environments all generate correct absolute URLs without code changes.
14. As a site operator, I want character pages to remain nonexistent and absent from the sitemap, so that player privacy is preserved exactly as decided previously.
15. As a site operator, I want documentation of the post-deployment Search Console steps, so that re-indexing actually starts once the feature ships.
16. As a developer, I want all crawlability behavior covered by handler tests driving real HTTP requests, so that any regression that breaks crawling fails in CI with an obvious cause.
17. As a developer, I want the sitemap built with proper XML marshalling, so that unusual world names can never produce malformed markup.
18. As a search-engine crawler, I want every sitemap URL to be absolute and match each page's canonical URL, so that discovery and consolidation agree with each other.

## Implementation Decisions

- **Indexable page set** (per the glossary term): dashboard, races, worlds index, one page per world from the current statistics snapshot, expansions, methodology. HTMX partials, the JSON API, and the privacy-disabled character pages are excluded by construction.
- **Root URL**: the root route renders the dashboard directly instead of redirecting; the existing dashboard path continues to serve as an alias. Both carry a canonical link pointing at the root URL. In-site navigation links are left unchanged.
- **Sitemap endpoint**: a new UI route serves the sitemap protocol document with an XML content type. URLs are absolute (base URL plus canonical path), marshalled through the standard XML encoder for safe escaping. Each URL carries `lastmod` taken from the snapshot's generation timestamp in RFC-3339 UTC. When no snapshot is available (cold or degraded statistics), the sitemap still responds successfully with the static pages and omits `lastmod`.
- **Base URL configuration**: a new application config key holds the public origin (scheme plus host). It defaults to the production domain in the embedded config and is overridable by environment variable under the existing config env-override rules. Canonical URLs, sitemap URLs, and Open Graph URLs are all derived from it.
- **Head metadata**: the shared page data structure gains canonical-path and description fields, populated by each page controller with its clean path (never the query-string-filtered variant) and concise summary copy. The shared layout renders: canonical link, meta description, `og:site_name`, `og:title`, `og:description`, `og:type`, `og:url`, and the favicon link.
- **Favicon**: a simple generated sword-on-dark icon consistent with the site branding is committed as an embedded static asset, served at the conventional favicon path with an image content type, and referenced from the layout head. It is authored at development time, not generated at runtime.
- **Crawl policy**: robots.txt remains Cloudflare-managed; the app serves no robots.txt and sets no `noindex`/`X-Robots-Tag` headers. Filter permutations are handled by canonical tags alone — this is a deliberate operator decision.
- **Documentation**: the UI and HTTP API docs gain the new endpoint, head-tag behavior, and the post-deployment Search Console checklist (submit sitemap URL, run live URL inspection, request re-indexing).

## Testing Decisions

- A good test asserts externally observable HTTP behavior only: status codes, response headers, and parseable properties of the body (canonical link present with the expected href, sitemap document parses as XML and contains exactly the expected URL set with expected `lastmod`, favicon returns non-empty image bytes). Tests never match full rendered HTML strings nor test internal helpers directly.
- **One test seam: the HTTP surface.** Handler tests drive real requests against the registered mux with in-memory snapshot fixtures, covering: root renders the dashboard, every page exposes correct canonical/description/OG tags, sitemap contents and degraded-snapshot behavior, and favicon serving.
- The base URL config key (default plus environment override) is covered by the existing config package tests.
- Prior art: the UI package's existing handler tests (route registration, dashboard rendering, world-detail rendering with snapshot fixtures) and the config package's environment-override tests.

## Out of Scope

- Serving robots.txt from the app or changing Cloudflare's managed robots.txt.
- Any `noindex`/`X-Robots-Tag` headers on partials, docs, or API routes.
- Per-race, per-datacenter, or per-expansion deep pages as separate indexable URLs.
- JSON-LD structured data beyond the site-name signal, hreflang, and Open Graph images.
- Character pages in any form (privacy-disabled by prior decision).
- Cloudflare configuration changes of any kind.
- Executing the Search Console submission and verification steps (operator actions, documented only).

## Further Notes

- The glossary gained the term **indexable page** for the crawl-scope concept defined here (committed separately).
- External verification before this spec: Googlebot/GPTBot/generic/empty user agents all receive clean 200 responses with no challenge header, and the managed robots.txt contains no disallow rules — the indexing stall is a discovery-and-signals gap, not a crawl block. The definitive post-deploy confirmation is Search Console's live URL inspection.
- The per-world page shape already exists; the sitemap only enumerates URLs the app already serves.
