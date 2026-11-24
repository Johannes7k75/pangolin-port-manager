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
- Detects unapplied port rules in the Compose override and static Traefik configuration.
- Recreates only the gateway services (`gerbil` and `traefik`) with `--no-deps` via the Docker socket without disrupting the rest of the stack.

---

## Features

- **Embedded Web Dashboard**: Fast, lightweight UI built with Go templates, HTMX, and a clean dark theme matching Pangolin's native aesthetic.
- **Full Port Lifecycle**: Create, edit, toggle, and delete port allocations with immediate live YAML preview.
- **Drift Detection**: Highlights when the Compose override or managed static entry points differ from configured rules and provides a one-click apply action. Optional Traefik API verification checks runtime entry points after applying.
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

Port Manager needs write access to the original static YAML **and its parent directory**, because updates use atomic file replacement and create a backup beside the file. The default `.:/compose` mount provides this access.

Mount the configuration **directory** into Traefik, for example `./config/traefik:/etc/traefik:ro`. Traefik can retain read-only access. Avoid mounting only the YAML file: a bind mount of an individual file can remain attached to an older file after atomic replacement. Mount the parent directory in Port Manager as well.

For custom project layouts, set `TRAEFIK_STATIC_CONFIG_PATH` explicitly to the original YAML file as seen inside the manager container. The default is `config/traefik/traefik_config.yml` under `COMPOSE_PROJECT_DIR`. This must be the same host file loaded by Traefik's original `--configFile` command.

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
```

Apply updates `entryPoints` directly in the original static Traefik YAML:

```yaml
entryPoints:
  web:
    address: ":80"
  # pangolin-port-manager: managed entry point
  tcp-25565:
    address: ":25565/tcp"
```

Port Manager adds an ownership comment to each entry point it creates. Disabling or deleting a rule removes only its marked entry point. Existing unmarked entries remain administrator-owned, including entries matching a port rule. Matching TCP addresses may omit the implicit `/tcp` suffix. Conflicting addresses cause Apply to fail.

Other settings, listener options, and YAML comments are preserved. YAML formatting may be normalized when managed entries change; an Apply with no entry-point changes leaves the file byte-for-byte unchanged. Before each modification, Port Manager saves the exact previous contents to `<static-config-path>.port-manager.bak`, then atomically replaces the original file while preserving its permission bits. Missing or malformed YAML is rejected before writing or recreating containers. Validation checks YAML structure and managed entry-point conflicts; it does not validate the complete Traefik configuration schema. TOML sources must be converted to YAML first.

You can edit the original static YAML and restart Traefik normally; no regeneration step or sidecar is needed. Unrelated manual edits do not require Apply and are not tracked against the running container. If you change a managed entry point's address, a subsequent Apply fails until you restore its address or remove its ownership comment. Removing that comment transfers ownership to you; Port Manager will then retain the entry when its rule is removed. Preserve ownership comments when editing or formatting the file if you want Port Manager to continue managing those entries.

**Upgrading from v0.1.1:** click Apply once after updating Port Manager. It writes the managed entry points into the original YAML, restores the original `--configFile` command, removes the generated snapshot mount from the override, and recreates Traefik. The old `docker-compose.ports.yml.traefik.yml` snapshot is left on disk unused; manual edits made only to that snapshot must be copied into the original YAML before applying.

Traefik's file, CLI, and environment static configuration methods are [mutually exclusive](https://doc.traefik.io/traefik/getting-started/configuration-overview/#the-install-configuration). In file mode, the generated command contains only the original `--configFile` argument; move other static settings from the base Compose command into the original YAML (including `api.dashboard` and `api.insecure` if needed). The base command is automatically read from the Traefik service in the original Compose file. CLI-only deployments without `--configFile` continue to receive managed entry-point flags.

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
| `TRAEFIK_STATIC_CONFIG_PATH` | `<COMPOSE_PROJECT_DIR>/config/traefik/traefik_config.yml` | Writable original static YAML path inside the manager; used in file mode |
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
