# Keep

Homelab backup orchestrator: consistent database dumps, Kopia snapshots, Lookout heartbeats.

Keep decides **when** and **how** the homelab is backed up. A proven engine
([Kopia](https://kopia.io) now, restic later) does the storage: chunking,
dedup, encryption, retention and the upload. Keep never touches any of
that. It makes each backup consistent, runs it, reports on it, and keeps
the history.

![Sources](docs/images/overview-desktop.png)

One small Go binary with the web UI built in, data in SQLite, ~12 MiB idle.

## What a run does

Every 12 hours (or on **Run now**, here or on Foyer's card), for each source:

1. **Prepare** it, by its strategy:
   - `sqlite` (the default): finds every SQLite database by its file header
     and copies it into staging with `VACUUM INTO`, a consistent copy even
     while the app writes. The live database and its `-wal`/`-shm` files are
     left out of the snapshot, and the copy goes in. If a database can't be
     copied, the live file stays in and the run warns.
   - `postgres` / `mariadb`: `pg_dumpall` / `mariadb-dump --single-transaction`
     inside the database's container, using the credentials it was started
     with, so Keep never holds them.
   - `stop`: stops the containers, snapshots, and starts again only those
     it stopped.
   - `files`: nothing to prepare.
2. **Snapshot** the folder and the staged copies through the engine. Keep
   turns the engine's own schedule off for these paths and sets their
   excludes.
3. **Report**: it records the run and pings a heartbeat (`/start`, then the
   URL itself, or `/fail`).

A source is **stale** when its last good backup is older than `stale_after`
(default: twice the interval plus an hour).

## Run it

Keep tells the engine "snapshot `/data/ledger`", and the engine reads its own
`/data/ledger`. So Keep and the engine's container must see every source,
and the staging folder, **at the same paths**. Keep checks this through
both containers' mounts before each snapshot and fails the source loudly
if they differ.

```yaml
services:
  keep:
    image: ghcr.io/audemed44/keep:latest
    restart: unless-stopped
    user: "1000:1000"
    group_add: ["988"] # the docker group's id: getent group docker
    environment:
      - KEEP_TOKEN=${KEEP_TOKEN} # openssl rand -hex 32
      - KEEP_DATA_DIR=/data/keep # keep.db, keep.yml and staging
      - KEEP_HEARTBEAT_URL=${KEEP_HEARTBEAT_URL}
    volumes:
      - ./:/data # the stack folder, as Kopia sees it too
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8091:8080"
    mem_limit: 64m
```

The stack folder is mounted read-write because SQLite has to touch a WAL
database's `-shm` file even to read it. Keep writes nothing else outside its
own folder. See [docker-compose.example.yml](docker-compose.example.yml).

| Variable | Default | |
|---|---|---|
| `KEEP_TOKEN` | (required) | What you sign in with; also Foyer's widget key |
| `KEEP_DATA_DIR` | `/data` | Where `keep.db` (and by default `keep.yml`) live |
| `KEEP_CONFIG` | `$KEEP_DATA_DIR/keep.yml` | The config file |
| `KEEP_HEARTBEAT_URL` | | A Lookout or healthchecks.io ping URL |
| `KEEP_DOCKER_SOCKET` | `/var/run/docker.sock` | |
| `KEEP_CONTAINER` | the hostname | Keep's own container, to find host paths |
| `KEEP_PORT` | `8080` | Port inside the container |
| `HOMEPAGE_URL` | | Foyer's address, linked from the header |
| `KEEP_DEBUG` | | Set to log debug messages |

## keep.yml

Written with comments on first start, and read again for every run, so
edits apply without a restart.

```yaml
every: 12h
engine: {type: kopia, container: kopia}
staging: /data/keep/staging
roots:            # every folder in a root is a source named after it
  - path: /data
    skip: [scripts]
excludes: ["*.sync-conflict-*"]
sources:          # extra sources, or changes to found folders (same name)
  - {name: romm, excludes: [/library]}
  - {name: docvault, path: /docvault}
  - {name: paperless-db, strategy: postgres, container: paperless-db, volume: paperless_pgdata}
```

Excludes use the part of gitignore syntax Kopia and restic read the same
way: `/x` from the top of the source, `name` at any depth, `dir/` for
folders only. `**` and `!` aren't supported. A source's excludes replace
Kopia's own ignore rules for that path, including any inherited from a
parent folder's policy.

## Foyer

Keep serves a [Foyer](https://github.com/audemed44/foyer) card at
`/api/foyer/widget` (last run, sources backed up, repository size, next run,
**Run now**), and `/api/foyer/backups` for Foyer's topology map: every
source as a host path or Docker volume, with its state.

```yaml
      - name: Keep
        url: https://keep.example.com
        container: keep
        widget:
          type: keep # or app, for the card alone
          url: http://keep:8080
          key: ${KEEP_TOKEN}
```

## Restoring

Restores go through the engine for now: Keep's sources are ordinary Kopia
snapshots (`kopia snapshot list /data/ledger`). A SQLite source's databases
are in the snapshot of `<staging>/<source>`, at the same relative paths, and
a database source's dump is `<staging>/<source>/<source>.sql`.

## Later: restic

The engine sits behind one interface (`internal/engine`), so restic can
replace Kopia: `restic backup --json`, `restic forget --prune` with the same
retention, `restic stats`. The plan: run both for about four weeks, check
restores, then switch and keep the Kopia repository read-only for a while.

## Development

```sh
cd frontend && npm install && npm run build && cd ..
KEEP_TOKEN=dev KEEP_DATA_DIR=./data go run ./cmd/keep
# or, with hot reload: run the binary, then `npm run dev` in frontend/
```
