# Homelab app template

The starting point for a new homelab app, with the same shape as Lookout,
Ledger and Parlor: a Go server with the Preact + TypeScript UI embedded, SQLite,
token sign-in, a Foyer card, the shared dark look, CI and a GHCR image.

It's a working app called **Skeleton** with one example resource
(**items**) that runs end to end: store → API → page → Foyer widget → tests.
Copy its patterns, then replace it.

## Start an app

```sh
gh repo create audemed44/<name> --public --template audemed44/homelab-app-template --clone
cd <name>
scripts/new-app.sh <name> "<Title>" <host port> "<one-line description>"
go mod tidy && (cd frontend && npm install)
```

`new-app.sh` replaces `skeleton` / `Skeleton` / `SKELETON` everywhere
(`pocket-log` gives `POCKET_LOG_TOKEN`), moves `cmd/skeleton`, sets the
host port, removes this file, `skill/` and itself, and fails if any
placeholder is left.

From Claude Code, `/new-homelab-app` does all of it, plus the repo settings
and wiring the app into the homelab: see [skill/new-homelab-app/SKILL.md](skill/new-homelab-app/SKILL.md).

## What's in it

| | |
|---|---|
| `cmd/skeleton/main.go` | env config, `healthcheck` subcommand, graceful shutdown, `HOMEPAGE_URL` |
| `internal/server/auth.go` | token as bearer or HMAC-derived session cookie, same-origin guard |
| `internal/server/server.go` | routes, JSON helpers, SPA fallback, security headers |
| `internal/server/foyer.go` | the Foyer widget (stats, items, actions) |
| `internal/server/items.go` | example CRUD handlers (replace) |
| `internal/store/` | SQLite (modernc, one connection, WAL), append-only migrations, settings KV |
| `frontend/src/App.tsx` | session check, sign-in, shell with Foyer back-link and nav |
| `frontend/src/components/ui.tsx` | Dialog, Field, Figure, SectionHead, CopyField, useAction |
| `frontend/src/styles.css` | the shared look: tokens, type, buttons, forms, lists, tables, dialogs |
| `.github/workflows/` | Conventional Commits, gofmt/vet/test, prettier/tsc/vitest/build; image to GHCR on main |
| `Dockerfile` | node → go → alpine, uid 1000, `GOMEMLIMIT=32MiB`, HEALTHCHECK |

The template repo itself never publishes an image (the Docker job skips
template repos).

## Changing the template

Improvements found while building an app (a better helper, a style fix)
belong here too, so the next app starts with them. Same rules as the apps:
branch, Conventional Commits, PR, rebase merge. Check a change still renames
cleanly:

```sh
tmp=$(mktemp -d) && git archive HEAD | tar -x -C "$tmp" && cd "$tmp" \
  && git init -q && scripts/new-app.sh pocket-log "Pocket Log" 8099 "Test rename" \
  && go vet ./... ; cd - && rm -rf "$tmp"
```

## Installing the skill

```sh
scripts/install-skill.sh   # symlinks skill/new-homelab-app into ~/.claude/skills
```
