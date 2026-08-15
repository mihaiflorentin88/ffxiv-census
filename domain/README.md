# Domain Layer

Business rules belong here. Structure packages by bounded context (e.g., `domain/user`, `domain/invoice`). Each domain package should expose rich services/entities and accept interfaces defined under `port/contract`.

Guidelines:

- Avoid direct dependencies on HTTP, CLI, or infrastructure packages.
- Prefer constructor functions that accept interfaces (ports) so collaborators can be mocked in tests.
- Keep DTO conversions at the edge (handlers or application services), not in the domain itself.
