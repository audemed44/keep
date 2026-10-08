# Keep

Homelab backup orchestrator: consistent database dumps, Kopia snapshots, Lookout heartbeats.

One small Go binary with the web UI built in, data in SQLite.

## Run it

```yaml
services:
  keep:
    image: ghcr.io/audemed44/keep:latest
    restart: unless-stopped
    user: "1000:1000"
    environment:
      - KEEP_TOKEN=${KEEP_TOKEN} # openssl rand -hex 32
    volumes:
      - ./keep:/data
    ports:
      - "8091:8080"
```

See [docker-compose.example.yml](docker-compose.example.yml) for every option.

| Variable | Default | |
|---|---|---|
| `KEEP_TOKEN` | (required) | What you sign in with; also Foyer's widget key |
| `KEEP_DATA_DIR` | `/data` | Where the database lives |
| `KEEP_PORT` | `8080` | Port inside the container |
| `HOMEPAGE_URL` | | Foyer's address, linked from the header |
| `KEEP_DEBUG` | | Set to log debug messages |

## Foyer

Keep serves a [Foyer](https://github.com/audemed44/foyer) card at
`/api/foyer/widget`:

```yaml
      - name: Keep
        url: https://keep.example.com
        container: keep
        widget:
          type: app
          url: http://keep:8080/api/foyer/widget
          key: ${KEEP_TOKEN}
```

## Development

```sh
cd frontend && npm install && npm run build && cd ..
KEEP_TOKEN=dev KEEP_DATA_DIR=./data go run ./cmd/keep
# or, with hot reload: run the binary, then `npm run dev` in frontend/
```
