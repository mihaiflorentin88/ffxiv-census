# Coding Style Guidelines

## Layer Interaction
- `cmd/` can construct domain objects directly. All other layers remain decoupled. Use the service container to resolve infrastructure dependencies and rely on interfaces instead of concrete types.
- Data flows through DTOs and contracts only; no layer crosses boundaries with raw structs from another layer.
- Domain code embraces OOP principles. Domain objects may encapsulate state and collaborate with infrastructure via contracts and DTOs, never concrete drivers.

## Design Practices
- Prefer structs with constructor-style functions; inject dependencies explicitly to keep code testable and maintain layer boundaries.
- Break functionality into small, reusable functions and favour working with a single aggregate object rather than many scattered values. Accept slices only when the behaviour is truly collection-centric.
- Reach for established design patterns when they clarify responsibilities or collaboration patterns.
- Follow the Go style guide: [https://google.github.io/styleguide/go/](https://google.github.io/styleguide/go/).
