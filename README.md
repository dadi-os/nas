# Nas

Nas is the OS and infrastructure layer. It owns the topology — what services exist, how they are networked, how names route, how they start, and how logs are collected. Every other module describes only what it depends on; Nas is the composition layer, so it is the only place the full system is written down.

## Topology

| Service | Image source | Internal address |
| --- | --- | --- |
| `caddy` | `caddy:2-alpine` | publishes host port 80 |
| `dwar` | build `../dwar`, target `dev` | `dwar:8080` |
| `yaad` | build `../yaad`, target `dev` | `yaad:8080` |
| `yaad-postgres` | `pgvector/pgvector:pg18` | `yaad-postgres:5432` |
| `dimaag` | build `../dimaag`, target `dev` | `dimaag:8080` |
| `dimaag-postgres` | `postgres:18` | `dimaag-postgres:5432` |
| `headscale` | `headscale/headscale:0.26` | publishes host port 8080 |
| `tailscale` | `tailscale/tailscale:latest` | mesh node `os` → forwards to Caddy |
| `nas-service` | build `./service`, target `dev` | `nas-service:8080`, host `8092` so Hath can provision |
| `bootstrap` | oneshot from `./service` | creates Headscale user + sidecar auth key |
| `loki` | `grafana/loki` | `loki:3100` log store |
| `alloy` | `grafana/alloy` | ships Docker + Hath logs → Loki |

One Docker network, `dadi`. Caddy publishes host port 80. Headscale publishes host port 8080 (see Mesh). Nas's HTTP API is published on host port 8092 only so Mac-side provisioning can curl it before the mesh is up; all other access goes through Caddy. Caddy holds network aliases for `dwar.dadi`, `yaad.dadi`, `dimaag.dadi`, and `nas.dadi` so containers resolve those names the same way the Mac does via `/etc/hosts`.

## Two runtimes, one topology

| Environment | Runtime | Definition |
| --- | --- | --- |
| Development | Docker Compose + Overmind on a Mac | `docker-compose.yml` + `Procfile` (this repo) |
| Production | podman + systemd on the box | quadlets in `/etc/containers/systemd/` (not yet); Watchtower for image updates |

Same services, same names, same routing. Only the runtime differs. Dev is what exists so far.

**Prod logging (later):** Alloy reads journald from podman/systemd units into the same Loki shape; `GET /logs` stays the client API. Watchtower restarts updated units; log identity is the module/unit name.

## Mesh

Two naming layers exist at once and must not be confused:

- **Docker DNS** — how containers reach each other. Caddy's `*.dadi` network aliases are this layer. `curl http://yaad.dadi/health` from the Mac via `/etc/hosts` → localhost:80 is still this path.
- **Headscale MagicDNS** — how tsnet clients (Hath) resolve `*.dadi`. Extra records in `headscale/config.yaml` point service names at the sidecar's mesh address. Separate namespace, separate mechanism. Neither replaces the other.

Headscale is the one exception to "only Caddy publishes a host port." A device that has not joined the mesh cannot resolve `.dadi` names, so the control server must be reachable by ordinary means (`localhost:8080` in dev). That is also why `control_url` lives in the provisioning bundle rather than as a constant — production swaps the address without a code change.

The Tailscale sidecar joins as hostname `os` (MagicDNS: `os.dadi`) and L3-forwards inbound mesh traffic to Caddy, which routes by Host header. Current Tailscale rejects `TS_DEST_IP` together with userspace mode, so the sidecar runs with kernel networking (`NET_ADMIN` + `/dev/net/tun`) and `TS_EXPERIMENTAL_DEST_DNS_NAME=caddy`.

**Sidecar must be the first node.** MagicDNS extra records assume the sidecar receives `100.64.0.1` (Headscale's first sequential allocation). If anything else registers first, those records are wrong — run `docker compose exec headscale headscale nodes list -o json`, put the sidecar's real address into `headscale/config.yaml` `dns.extra_records`, and restart Headscale. Prefer keeping the sidecar first over automating around the assumption.

`bootstrap` is a oneshot that runs on every `docker compose up`: create user `ankur` if missing, mint a reusable sidecar pre-auth key only if `/run/bootstrap/authkey` is absent. Idempotent by design. `./down --wipe` wipes `headscale_data`, `tailscale_state`, `bootstrap`, Postgres, Loki, and Hath credentials, and the whole mesh rebuilds cleanly.

### Status errors in dev

`GET /status` returns `"errors": []` under Docker. Searchable errors live at `GET /logs`. Do not substitute `docker logs` into status — one honest empty list beats two diverging code paths.

## Logging

All modules emit **JSON lines on stdout** with at least `time`, `level`, `service`, and `msg`.

| Path | Role |
| --- | --- |
| Terminal | Overmind multiplexes Compose + Hath for live tails |
| Alloy | Reads Docker container logs + `nas/.run/hath.log` |
| Loki | Stores and indexes logs |
| `GET /logs` on nas-service | LogQL query API for Hath (filter by `services`, `level`, `q`, time range) |

Example:

```sh
curl -sG 'http://nas.dadi/logs' \
  --data-urlencode 'services=dwar,yaad' \
  --data-urlencode 'level=error' \
  --data-urlencode 'q=timeout'
```

`LOKI_URL` is required on nas-service (compose sets `http://loki:3100`). Hath sets `HATH_LOG_FILE` to `nas/.run/hath.log` when launched via `./up`.

## One-time host setup

**`/etc/hosts` is required.** Without it the stack looks broken — nothing on the host can resolve `.dadi` via Caddy. Add this line:

```
127.0.0.1  dwar.dadi yaad.dadi dimaag.dadi nas.dadi
```

`./up` checks for a `.dadi` line and exits with that exact entry if it is missing.

**Overmind** (and **tmux**) are required for the unified bring-up:

```sh
brew install overmind tmux
```

## Bring-up

```sh
./up
```

Preflight (env files, hosts, docker, overmind), then Overmind starts:

1. `stack` — `docker compose up --build` (migrations as oneshots, healthchecks, Loki/Alloy)
2. `hath` — waits for `*.dadi` health, provisions credentials if needed, runs `npm run tauri dev`

Ctrl-C stops Overmind (Compose attach + Hath together). Containers may still be running until `./down`.

```sh
./down           # stop containers
./down --wipe    # also destroy volumes and Hath's credentials
```

Hath is a native process in both environments — it is not containerized anywhere. In prod it is the Plymouth/kiosk desktop under cage; in dev Overmind launches Tauri on the Mac.

Dev has no external connectivity by design. Headscale stays on localhost; a phone joining from cellular is a production concern and changes one value in the provisioning bundle when it arrives.

Each module owns its `.env` (gitignored). Nas points `env_file` at those files. `./up` copies from `.env.example` when a file is missing — Dwar's may stay empty for a stack that does not need model calls (Dwar boots and returns `503 provider_unconfigured` on routes that need a key). Database passwords and URLs live in each module's `.env`; `POSTGRES_USER` / `POSTGRES_DB` stay inline in compose as topology.

## Working on one module

Edit files in the sibling directory (`../yaad`, `../dimaag`, `../dwar`, …). Bind mounts + watchers pick up changes:

| Module | Hot reload |
| --- | --- |
| yaad / dimaag | bind mount + `tsx watch` (dimaag also watches `prompts/`) |
| dwar | bind mount + `uvicorn --reload`; lane prompts re-read on mtime |
| nas-service | bind mount + `air` |
| hath | Vite HMR / Tauri rebuild |

Start a subset when you only need containers:

```sh
docker compose up yaad yaad-postgres
```

## Not here yet

Bootc image, quadlets, LUKS, remote (off-LAN) mesh join, Watchtower, journald→Alloy on the box, Hath logs widget UI.
