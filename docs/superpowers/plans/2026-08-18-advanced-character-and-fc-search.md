# Plan: Advanced Character & Free Company Search API and UI

## Implementation Steps
1. [x] Extend contracts `CharacterFilter`, `FreeCompanyFilter`, and `FreeCompanyRepository` in `port/contract/`.
2. [x] Update SQLite and mock repository implementations for Character and FreeCompany.
3. [x] Write comprehensive unit tests for Character and Free Company repository filters and sorting in `infrastructure/sqlite/repository/`.
4. [x] Extend `cmd/http/app/census/handler/census.go` to handle new character query parameters.
5. [x] Create `cmd/http/app/census/handler/free_company.go` and mount REST routes in `cmd/http/app/census/routes.go`.
6. [x] Write unit tests for character and FC REST controllers.
7. [x] Update `cmd/http/ui/character.go` and `templates/characters_list.html` with advanced filter controls.
8. [x] Implement Free Company UI controller in `cmd/http/ui/free_company.go` and HTML templates `free_companies_list.html` and `free_company_detail.html`.
9. [x] Mount FC UI routes in `cmd/http/ui/routes.go` and update navigation in `layout.html`.
10. [x] Add UI controller unit tests for Free Company list and detail handlers.
11. [x] Update Swagger YAML and JSON OpenAPI specifications.
12. [x] Verify complete test suite with `go test -v -race ./...`, `make lint`, and `make build`.
