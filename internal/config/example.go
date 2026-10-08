package config

// Example is the keep.yml written on first start.
const Example = `# Keep: what to back up, how, and how often. Read again for every run,
# so edits apply without a restart. Paths are as Keep sees them; the
# engine's container must see them at the same paths.

# Time between scheduled runs. "Run now" works any time.
every: 12h
# A source is stale when its last good backup is older than this
# (default: twice "every" plus an hour).
# stale_after: 25h

engine:
  type: kopia       # restic is planned
  container: kopia  # Keep runs the kopia CLI in this container

# Database copies and dumps go here before their snapshot; the engine
# must see this folder at the same path too.
staging: /data/keep/staging

# Every folder in a root becomes a source named after it, so a new app's
# data is backed up without editing this file. SQLite databases in it are
# found by their header and copied consistently.
roots: []
#  - path: /data
#    skip: [scripts]

# Patterns left out of every source: /x from the top of the source,
# name at any depth, dir/ for folders only.
excludes:
  - "*.sync-conflict-*"

# Extra sources, or changes to found folders (same name).
sources: []
#  - name: romm            # a found folder: leave its ROMs out
#    excludes: [/library]
#  - name: old-app
#    skip: true
#  - name: documents       # a folder outside the roots
#    path: /docvault
#  - name: paperless-db    # dump a database through its container
#    strategy: postgres    # or mariadb
#    container: paperless-db
#    volume: main-server_paperless_pgdata  # what the dump covers, for Foyer
#  - name: cloudbeaver     # no safe dump: stop it for the snapshot
#    path: /data/cloudbeaver
#    strategy: stop
#    container: cloudbeaver
`
