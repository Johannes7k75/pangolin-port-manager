# Stage 1: Build binary
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /pangolin-provision ./cmd/server

# Stage 2: Minimal runtime image with docker-cli & docker-compose
FROM alpine:3.21

RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    docker-cli \
    docker-cli-compose

WORKDIR /app

COPY --from=builder /pangolin-provision /app/pangolin-provision

# Default volumes
# /data: JSON store (ports.json)
# /compose: Project directory containing docker-compose.yml and output docker-compose.ports.yml
# /var/run/docker.sock: Docker daemon socket

ENV PORT=8080 \
    DATA_DIR=/data \
    PORTS_FILE_PATH=/data/ports.json \
    COMPOSE_PROJECT_DIR=/compose \
    COMPOSE_OUT_PATH=/compose/docker-compose.ports.yml \
    COMPOSE_FILES=docker-compose.yml,docker-compose.ports.yml \
    GERBIL_SERVICE_NAME=gerbil \
    TRAEFIK_SERVICE_NAME=traefik \
    TRAEFIK_BASE_COMMANDS="--configFile=/etc/traefik/traefik_config.yml,--api.insecure=true,--api.dashboard=true"

EXPOSE 8080

ENTRYPOINT ["/app/pangolin-provision"]
