# Skeleton

Instructions for coding agents working in this repository. `CLAUDE.md`
imports this file.

## Project

TODO one line on what it does. A Go server (`cmd/skeleton`, `internal/`)
serves a JSON API and the Preact + TypeScript frontend (`frontend/`), built
into `web/dist` and embedded in the binary. State lives in SQLite at
`/data/skeleton.db` (`internal/store`).

- `internal/server`: routes (`server.go`), token sign-in and the
  same-origin guard (`auth.go`), the Foyer card (`foyer.go`).
- `internal/store`: schema, migrations (append-only, tracked in
  `PRAGMA user_version`) and queries.
- `frontend/src`: `App.tsx` (session, shell, nav), `router.ts` (path
  routes), `api.ts` (one function per endpoint), `components/ui.tsx`
  (Dialog, Field, Figure, SectionHead, useAction), `styles.css`.

## Constraints

- **Low memory is a feature.** One static binary, `GOMEMLIMIT=32MiB`,
  `mem_limit: 64m` in compose. Keep history in SQLite, not in memory.
- Direct dependencies: modernc.org/sqlite (pure Go, so the build stays
  static and cgo-free). Justify any new one, Go or npm.
- Every `/api/` call needs `SKELETON_TOKEN` (bearer, or the session cookie
  derived from it), and state-changing browser requests from another origin
  are refused (`sameOrigin`). Never log or return secrets.
- `GET /api/foyer/widget` serves the card in Foyer's widget format
  (https://github.com/audemed44/foyer/blob/main/docs/app-widgets.md).
- `HOMEPAGE_URL` puts a link back to Foyer in the header.
- UI style is Foyer's: Swiss editorial, always dark (no light theme), heavy
  Inter headlines, tracked uppercase eyebrows, 2px rules over numbered
  headings, square corners, one accent (#2563ff). Check phone width too
  (390px): it's used from an iPhone.

## Commits

Conventional Commits: `<type>(<scope>): <summary>`, e.g. `feat(items): ...`.
CI rejects anything else, including the PR title.

## Checks before pushing

```sh
go vet ./... && go test -race ./...        # needs web/dist (npm run build)
cd frontend && npm run format:check && npm run typecheck && npm test && npm run build
docker build -t skeleton:dev .
```
