# Skeleton

> The homelab app template: see [TEMPLATE.md](TEMPLATE.md).

TODO one line on what it does.

One small Go binary with the web UI built in, data in SQLite.

## Run it

```yaml
services:
  skeleton:
    image: ghcr.io/audemed44/skeleton:latest
    restart: unless-stopped
    user: "1000:1000"
    environment:
      - SKELETON_TOKEN=${SKELETON_TOKEN} # openssl rand -hex 32
    volumes:
      - ./skeleton:/data
    ports:
      - "8089:8080"
```

See [docker-compose.example.yml](docker-compose.example.yml) for every option.

| Variable | Default | |
|---|---|---|
| `SKELETON_TOKEN` | (required) | What you sign in with; also Foyer's widget key |
| `SKELETON_DATA_DIR` | `/data` | Where the database lives |
| `SKELETON_PORT` | `8080` | Port inside the container |
| `HOMEPAGE_URL` | | Foyer's address, linked from the header |
| `SKELETON_DEBUG` | | Set to log debug messages |

## Foyer

Skeleton serves a [Foyer](https://github.com/audemed44/foyer) card at
`/api/foyer/widget`:

```yaml
      - name: Skeleton
        url: https://skeleton.example.com
        container: skeleton
        widget:
          type: app
          url: http://skeleton:8080/api/foyer/widget
          key: ${SKELETON_TOKEN}
```

## Development

```sh
cd frontend && npm install && npm run build && cd ..
SKELETON_TOKEN=dev SKELETON_DATA_DIR=./data go run ./cmd/skeleton
# or, with hot reload: run the binary, then `npm run dev` in frontend/
```
