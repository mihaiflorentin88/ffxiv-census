# Spec: Advanced Character & Free Company Search API and UI

## Problem Statement
Users and analysts of `ffxiv-census` need rich query capabilities for filtering characters (by Grand Company, active status, and Free Company affiliation) and exploring Free Companies (guilds) with directory search, sorting, and detail views.

## Solution Architecture
1. **Contracts & Filtering**:
   - `CharacterFilter` extended with `GrandCompany`, `FreeCompanyID`, `ActiveOnly`, `SortBy`, and `SortOrder`.
   - `FreeCompanyFilter` defined with `World`, `Datacenter`, `Name`, `Tag`, `GrandCompany`, `SortBy`, `SortOrder`.
   - `FreeCompanyRepository` extended with `List` and `Count`.
2. **SQLite Repository**:
   - Dynamic WHERE clause and safe whitelisted ORDER BY generation for characters and free companies.
3. **REST Endpoints**:
   - `GET /api/v1/census/characters`: extended query params (`grand_company`, `free_company_id`, `active`, `sort_by`, `sort_order`).
   - `GET /api/v1/census/free-companies`: list free companies with pagination and filtering.
   - `GET /api/v1/census/free-companies/{id}`: free company detail.
4. **Web UI**:
   - `/ui/characters`: Advanced search panel with Grand Company selector, Active-only toggle, and Sort By options.
   - `/ui/free-companies`: Free Company directory with search, sorting, and pagination.
   - `/ui/free-companies/{id}`: Free Company profile with known tracked members list.
   - Navigation header updated to include Free Companies.
5. **Swagger & OpenAPI**:
   - Full OpenAPI 2.0 specifications updated in `swagger.yaml` and `swagger.json`.
