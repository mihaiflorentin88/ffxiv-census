# Cascading UI Filters — Races & Worlds Pages

## Objective

Fix filter dropdowns on `/ui/races` and `/ui/worlds` so that selecting a higher-level
filter (Region) cascades to narrow lower-level filters (Datacenter, World).

## Problem

Both pages display independent filter dropdowns that always show all options regardless
of parent filter selection. Selecting Region=EU on the races page still shows all
datacenters (Aether, Chaos, Elemental, etc.) — users can select nonsensical combinations.

## Solution

Add `DCsForRegion(region)` and `WorldsForDC(dc)` helper functions to `world_data.go` that
derive filtered, sorted lists from the existing `worldDatacenter` map. Update both handlers
to use these helpers when building dropdown option lists.

## Hierarchy

Region > Datacenter > World (each level narrows the next).

## Affected Pages

- `/ui/races` — 3 cascading selects: Region, Datacenter, World
- `/ui/worlds` — Region buttons + Datacenter select: Region narrows DC list

## Constraints

- Server-side filtering via full page reload (`onchange="this.form.submit()"`). No HTMX partial swaps or client-side JS — matches existing pattern.
- Static `worldDatacenter` map drives filter options. If Square Enix adds worlds/DCs, the map must be updated (pre-existing concern).
