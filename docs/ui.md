# UI Skeleton

The wizard provisioned a tiny HTMX-esque dashboard for exploration.

- Static assets live in `cmd/http/ui/assets`.
- Templates are embedded via Go's `embed` package.
- Routes mount under `/ui/*`; adjust `registerUI` in `cmd/http/ui/routes.go`.

Replace everything with your real front-end when ready.
