# Keep

Instructions for coding agents working in this repository. `CLAUDE.md`
imports this file.

## Project

Homelab backup orchestrator: it makes each source consistent (SQLite
copies with VACUUM INTO, Postgres/MariaDB dumps, or stopping containers),
has the backup engine (Kopia, via `docker exec`) snapshot it, records the
run and pings a Lookout heartbeat. Runs are jobs of a kind: `backup`,
`verify` (weekly repository check, its own heartbeat, refreshes the
snapshot listing and deletes retired paths) and `restore` (into the
restores folder, never in place). With a local copy set, each backup also
snapshots every source into a second repository in a local folder
(`runner/local.go`, the same engine with its own `--config-file`); a
failure there is a warning, never a failed source, and restores use it
when the backup is in it. A Go server (`cmd/keep`, `internal/`)
serves a JSON API and the Preact + TypeScript frontend (`frontend/`), built
into `web/dist` and embedded in the binary. State lives in SQLite at
`$KEEP_DATA_DIR/keep.db` (`internal/store`), including the settings (what
to back up, edited in the UI; a v1 `keep.yml` is imported once).

- `internal/config`: the settings (validation, keep.yml import), folder
  discovery in roots, exclude patterns.
- `internal/runner`: the schedule, one job at a time (`backup.go`: hooks
  and prepare every source → read due policies in one call, set changed
  ones → one batched snapshot → after hooks → record; `verify.go`;
  `restore.go`; `retire.go`: snapshots of paths Keep doesn't manage), the
  path check between Keep's and the engine's mounts, the overview, folder
  browsing and suggestions from container mounts (`discover.go`).
- `internal/prepare`: SQLite discovery and copies, database dumps.
- `internal/engine`: the `Engine` interface and Kopia. Nothing outside this
  package knows how Kopia is called.
- `internal/docker`: exec, stop, start and inspect over the socket.
- `internal/server`: routes (`server.go`), token sign-in and the
  same-origin guard (`auth.go`), the Foyer card and `/api/foyer/backups`
  (`foyer.go`).
- `internal/store`: schema, migrations (append-only, tracked in
  `PRAGMA user_version`) and queries.
- `frontend/src`: `App.tsx` (session, shell, nav), `router.ts` (path
  routes), `api.ts` (one function per endpoint), `components/ui.tsx`
  (Dialog, Field, Figure, SectionHead, useAction), `styles.css`.

## Constraints

- **Low memory is a feature.** One static binary, `GOMEMLIMIT=32MiB`,
  `mem_limit: 64m` in compose. Keep history in SQLite, not in memory.
- Direct dependencies: modernc.org/sqlite (pure Go, so the build stays
  static and cgo-free) and gopkg.in/yaml.v3 (keep.yml is hand-edited, with
  comments). Justify any new one, Go or npm.
- **Keep never does chunking, encryption or storage itself**, and never
  writes to an app's files: only to its own folder, staging, restores and
  the local copy's folder (only when it's empty or a repository already).
  Restores never go in place. Restore browsing and downloads must stay
  inside the restore folder (`inRestore` resolves symlinks). Engine
  output is read from the CLI's `--json`, not Kopia's undocumented API.
- The local repository has its own cache, set with `KOPIA_CACHE_DIRECTORY`
  on every command (`Kopia.argv`): the container sets that variable for its
  own repository and Kopia prefers it to the config file's cache. A shared
  cache made a run treat the main repository as empty. `Open` checks the
  opened repository's unique ID against the folder's format file.
- Keep and the engine see every path the same way (same mount paths); the
  runner checks it. Kopia applies a parent folder's ignore rules to
  snapshots inside it unless the path has its own list, so `Configure`
  always sets one.
- **Engine calls are slow** (each opens the repository: 20–50 s over
  rclone), and each file read from Drive is ~5 s. Batch them; never add a
  per-source engine call to a run. Pages read Keep's copy of the snapshot
  list (the `snapshots` table), never the engine.
- Tests never touch a real repository: use the fake engine/docker in
  `runner_test.go`, or a throwaway `kopia/kopia` container with a
  filesystem repository.
- Every `/api/` call needs `KEEP_TOKEN` (bearer, or the session cookie
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
docker build -t keep:dev .
```
