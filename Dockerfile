# ── Frontend ────────────────────────────────────────────────────────────────
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ── Server ──────────────────────────────────────────────────────────────────
FROM golang:1.27-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/
COPY --from=frontend /src/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /skeleton ./cmd/skeleton

# ── Runtime ─────────────────────────────────────────────────────────────────
FROM alpine:3.23
RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 1000 -s /sbin/nologin skeleton \
    && mkdir -p /data && chown 1000:1000 /data
# In PATH, so `docker exec skeleton skeleton <command>` works.
COPY --from=server /skeleton /usr/local/bin/skeleton
ENV SKELETON_DATA_DIR=/data \
    SKELETON_PORT=8080 \
    GOMEMLIMIT=32MiB
USER 1000:1000
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["skeleton", "healthcheck"]
ENTRYPOINT ["skeleton"]
