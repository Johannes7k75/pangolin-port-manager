# Pangolin Port Manager

[![Build Status](https://github.com/Johannes7k75/pangolin-port-manager/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/Johannes7k75/pangolin-port-manager/actions/workflows/docker-publish.yml)
[![Container](https://img.shields.io/badge/GHCR-docker%20image-blue?logo=docker)](https://github.com/Johannes7k75/pangolin-port-manager/pkgs/container/pangolin-port-manager)
[![Go Version](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A lightweight companion service for [Pangolin](https://github.com/fosrl/pangolin) stacks that dynamically manages raw TCP and UDP port exposure on Gerbil exit nodes and provisions matching Traefik entrypoints with zero manual Compose edits.

---

## Overview

In a standard Pangolin deployment, Traefik shares the Gerbil WireGuard gateway's network namespace (`network_mode: service:gerbil`). Because Docker does not allow containers using a shared network stack to publish ports directly:

1. **Host Port Bindings** must be declared exclusively on the `gerbil` gateway container (`ports: ["25565:25565/tcp"]`).
2. **Traefik Listeners** require corresponding CLI entrypoint flags to accept raw TCP/UDP streams (`--entryPoints.tcp-25565.address=:25565/tcp`).

Pangolin Port Manager automates this entire lifecycle:
- Provides an embedded, zero-dependency Web UI designed to sit seamlessly behind Pangolin authentication.
- Maintains a persistent store of TCP and UDP port allocations with custom names, protocols, and enable/disable toggles.
- Generates a declarative Compose override file (`docker-compose.ports.yml`).
- Automatically detects configuration drift between desired state and running containers.
- Recreates only the gateway services (`gerbil` and `traefik`) with `--no-deps` via the Docker socket without disrupting the rest of the stack.

---

## Features

- **Embedded Web Dashboard**: Fast, lightweight UI built with Go templates, HTMX, and a clean dark theme matching Pangolin's native aesthetic.
- **Full Port Lifecycle**: Create, edit, toggle, and delete port allocations with immediate live YAML preview.
- **Drift Detection**: Highlights when active running containers differ from configured rules and provides a one-click apply action.
- **Graceful Async Recreation**: Recreates containers in the background with auto-polling status banners, avoiding gateway timeout resets when applied through Traefik.
- **Host Path Detection**: Automatically discovers the container's host project directory via Docker inspection to preserve relative bind mounts (e.g. `./config:/var/config`).
- **RESTful API**: Clean JSON endpoints for external automation and scripts.

---

## Quick Start

### Docker Compose

Add `pangolin-port-manager` to your existing Pangolin `compose.yml`:

```yaml
services:
  # ... existing pangolin, gerbil, and traefik services ...

  pangolin-port-manager:
    image: ghcr.io/johannes7k75/pangolin-port-manager:latest
    container_name: pangolin-port-manager
    restart: unless-stopped
    environment:
      - PORT=8080
      - COMPOSE_PROJECT_DIR=/compose
      - COMPOSE_OUT_PATH=/compose/docker-compose.ports.yml
      - COMPOSE_FILES=compose.yml,docker-compose.ports.yml
      - GERBIL_SERVICE_NAME=gerbil
      - TRAEFIK_SERVICE_NAME=traefik
    volumes:
      - ./data/provisioner:/data
      - .:/compose
      - /var/run/docker.sock:/var/run/docker.sock
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.portmanager.rule=Host(`ports.yourdomain.com`)"
      - "traefik.http.routers.portmanager.entrypoints=websecure"
      - "traefik.http.routers.portmanager.tls.certresolver=letsencrypt"
      - "traefik.http.services.portmanager.loadbalancer.server.port=8080"
```

Start your stack:

```bash
docker compose -f compose.yml -f docker-compose.ports.yml up -d
```

Access the dashboard at `https://ports.yourdomain.com` (or `http://localhost:8080` locally).

---

## Volume Configuration

The service uses three volume mounts:

| Mount | Purpose |
|---|---|
| `./data/provisioner:/data` | Persistent store for configured port rules (`ports.json`). |
| `.:/compose` | Allows the container's internal Compose CLI to read the base configuration and write `docker-compose.ports.yml`. |
| `/var/run/docker.sock:/var/run/docker.sock` | Docker daemon socket used to trigger container recreation (`up -d --no-deps gerbil traefik`). |

### Least-Privilege Setup

To avoid mounting the entire project root directory into the container, you can mount only the specific compose files:

```bash
# Ensure the override file exists before starting
touch docker-compose.ports.yml
```

```yaml
    volumes:
      - ./data/provisioner:/data
      - ./compose.yml:/compose/compose.yml:ro
      - ./docker-compose.ports.yml:/compose/docker-compose.ports.yml
      - /var/run/docker.sock:/var/run/docker.sock
```

---

## Generated Override Example

When port rules are defined (e.g. Minecraft on port `25565/tcp`), the service writes `docker-compose.ports.yml`:

```yaml
# Generated by Pangolin Port Manager. DO NOT EDIT DIRECTLY.
services:
  gerbil:
    ports:
      - "25565:25565/tcp"

  traefik:
    command:
      - "--configFile=/etc/traefik/traefik_config.yml"
      - "--api.insecure=true"
      - "--api.dashboard=true"
      - "--entryPoints.tcp-25565.address=:25565/tcp"
```

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP port the server listens on |
| `DATA_DIR` | `./data` | Directory for persistent storage (`ports.json`) |
| `PORTS_FILE_PATH` | `./data/ports.json` | Explicit path to the ports JSON file |
| `COMPOSE_PROJECT_DIR` | `/compose` | Project directory for `docker compose` commands |
| `COMPOSE_OUT_PATH` | `./docker-compose.ports.yml` | Output path for the generated Compose override file |
| `COMPOSE_FILES` | `compose.yml,docker-compose.ports.yml` | Comma-separated list of compose files passed with `-f` |
| `GERBIL_SERVICE_NAME` | `gerbil` | Name of the Gerbil service in your Compose configuration |
| `TRAEFIK_SERVICE_NAME` | `traefik` | Name of the Traefik service in your Compose configuration (base command arguments are automatically extracted from your base compose file) |

---

## REST API Reference

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/` | Web UI Dashboard |
| `GET` | `/healthz` | Health check endpoint (`200 OK`) |
| `GET` | `/api/ports` | List all configured port rules as JSON |
| `POST` | `/api/ports` | Create a new port rule (`{ "name": "Minecraft", "port": 25565, "protocol": "tcp", "enabled": true }`) |
| `PUT` | `/api/ports/{id}` | Update an existing rule's name, port, protocol, or state |
| `POST` | `/api/ports/{id}/toggle` | Toggle rule enabled/disabled state |
| `DELETE` | `/api/ports/{id}` | Delete a port rule |
| `POST` | `/api/apply` | Generate Compose override and trigger container recreation |

---

## Development & Testing

### Running Tests

Run the full Go test suite:

```bash
go test -v ./...
```

Run end-to-end browser tests via Playwright:

```bash
python3 -m unittest tests/playwright_e2e.py
```

### Building Locally

Build the Go binary:

```bash
go build -o bin/pangolin-port-manager ./cmd/server
```

Build the container image:

```bash
docker build -t pangolin-port-manager .
```

---

## License

This project is licensed under the [MIT License](LICENSE).
