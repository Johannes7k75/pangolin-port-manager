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
2. **Traefik Listeners** require matching entry points in the static configuration used at startup. With `--configFile`, these must be defined in that file; CLI-only deployments use entry-point flags.

Pangolin Port Manager automates this entire lifecycle:
- Provides an embedded, zero-dependency Web UI designed to sit seamlessly behind Pangolin authentication.
- Maintains a persistent store of TCP and UDP port allocations with custom names, protocols, and enable/disable toggles.
- Generates a declarative Compose override file (`docker-compose.ports.yml`).
- Detects drift between desired rules and generated files, including changes to the source Traefik configuration.
- Recreates only the gateway services (`gerbil` and `traefik`) with `--no-deps` via the Docker socket without disrupting the rest of the stack.

---

## Features

- **Embedded Web Dashboard**: Fast, lightweight UI built with Go templates, HTMX, and a clean dark theme matching Pangolin's native aesthetic.
- **Full Port Lifecycle**: Create, edit, toggle, and delete port allocations with immediate live YAML preview.
- **Drift Detection**: Highlights when generated configuration files differ from configured rules and provides a one-click apply action. Optional Traefik API verification checks runtime entry points after applying.
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
      - TRAEFIK_STATIC_CONFIG_PATH=/compose/config/traefik/traefik_config.yml
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

Start the manager with `docker compose -f compose.yml up -d pangolin-port-manager`, add rules in the dashboard, then click **Apply changes**. Apply generates the files and recreates Gerbil and Traefik. For subsequent manual stack operations, include the generated override:

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

### Static Configuration Access

Mount the project directory for generated files and the source static YAML read-only at a separate path. Set `TRAEFIK_STATIC_CONFIG_PATH` to that path. The manager writes the generated static YAML beside the Compose override, so that directory must be writable and visible to the host Docker daemon. Mounting only the override file is insufficient: atomic writes replace files and Apply also creates a second file.

For custom project layouts, set `TRAEFIK_STATIC_CONFIG_PATH` explicitly to the original YAML file as seen inside the manager container. The default is `config/traefik/traefik_config.yml` under `COMPOSE_PROJECT_DIR`. Keep `COMPOSE_OUT_PATH` in the mounted project directory.

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
      - "--configFile=/etc/traefik/port-manager-static.yml"
    volumes:
      - type: bind
        source: "./docker-compose.ports.yml.traefik.yml"
        target: /etc/traefik/port-manager-static.yml
        read_only: true
```

The adjacent `docker-compose.ports.yml.traefik.yml` contains the original static YAML settings plus:

```yaml
entryPoints:
  # Existing entry points and their options are retained.
  tcp-25565:
    address: ":25565/tcp"
```

Every Apply rebuilds this file from the original YAML. Disabling or deleting a rule removes its generated entry point. Entry points already present in the original file remain administrator-owned; a conflicting address causes Apply to fail. The original file is never modified. Malformed or missing source YAML also causes Apply to fail before container recreation. TOML sources must be converted to YAML first.

Traefik's file, CLI, and environment static configuration methods are [mutually exclusive](https://doc.traefik.io/traefik/getting-started/configuration-overview/#the-install-configuration). In file mode, the generated command contains only `--configFile`; move any other static settings from the base Compose command into the original YAML (including `api.dashboard` and `api.insecure` if needed). Existing providers, certificates, plugins, and web entry points are carried into the generated file. The base command is automatically read from the Traefik service in the original Compose file. CLI-only deployments without `--configFile` continue to receive managed entry-point flags.

To verify loaded listeners after Apply, set `TRAEFIK_API_URL` to the reachable Traefik API origin, for example `http://gerbil:8080` when the existing configuration exposes an internal insecure API. The manager polls `/api/entrypoints` for up to 15 seconds and fails Apply if any enabled rule's name or address is missing. No API is enabled or published automatically. With this variable unset, Apply checks Compose execution only and reports that runtime entry points were not verified. API verification checks listeners; it does not verify Pangolin router status or external connectivity.

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
| `TRAEFIK_SERVICE_NAME` | `traefik` | Name of the Traefik service in your Compose configuration |
| `TRAEFIK_STATIC_CONFIG_PATH` | `<COMPOSE_PROJECT_DIR>/config/traefik/traefik_config.yml` | Original static YAML path inside the manager; used in file mode |
| `TRAEFIK_API_URL` | unset | Traefik API origin for runtime entry-point verification after Apply |

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
