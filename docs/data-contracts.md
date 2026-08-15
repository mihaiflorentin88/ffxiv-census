# Data Contracts

This repository follows a strict DTO strategy to keep ports clean and stable.

## Directory Layout

- `port/dto/request` — inbound request payloads (HTTP JSON, CLI flags, etc.).
- `port/dto/response` — outbound representations returned to clients.
- `port/dto/internal` — internal transfer objects for background jobs or cross-layer communication.

## Guidelines

1. Keep DTOs dumb; validate and convert them before hitting the domain layer.
2. Avoid leaking domain structs to transports; map domain entities to DTOs in handlers.
3. When contracts change, document the update in this file and bump the API version in Swagger.

This setup preserves the hexagonal boundary: transports talk DTOs, domain services talk rich types.
