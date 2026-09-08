# Nas

Nas is the OS and infrastructure layer. It owns the topology — what services exist, how they are networked, how names route, how they start, and how logs are collected. Every other module describes only what it depends on; Nas is the composition layer, so it is the only place the full system is written down.

**One exported image:** `ghcr.io/dadi-os/nas` (bootc). Infra (Headscale, host Tailscale, Caddy, Loki, Alloy, control plane) is baked into that image and updates with `bootc upgrade` + reboot. App modules (`dwar`, `yaad`, `dimaag`) stay as containers and update via `podman-auto-update` with no reboot.

## Topology

### Production (host + app containers)

| Unit | Kind | Notes |
| --- | --- | --- |
| `headscale` | host systemd | control plane; `:8080` (tunnel / clients) |
| `bootstrap` | host oneshot | Headscale user + host mesh auth key |
| `tailscaled` + `dadi-tailscale` | host | mesh node `os` → MagicDNS `os.dadi` |
| `caddy` | host systemd | `:80`, Host-header → localhost app ports |
| `nas` | host systemd | control API on `127.0.0.1:8092` |
| `loki` / `alloy` | host systemd | logs |
| `cloudflared` | host systemd | tunnel to Headscale |
| `dwar` / `yaad` / `dimaag` (+ postgres / migrate) | podman quadlets | `AutoUpdate=registry`; published on `127.0.0.1:8081–8083` |

App containers share podman network `dadi` for DB DNS. They resolve `*.dadi` to the host gateway so `http://dwar.dadi` still goes through host Caddy (no `:port` in app URLs).

### Development (Mac Compose)

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
| `nas-service` | build `./service`, target `dev` | `nas-service:8080`, host `8092` |
| `bootstrap` | oneshot from `./service` | creates Headscale user + sidecar auth key |
| `loki` | `grafana/loki` | `loki:3100` log store |
| `alloy` | `grafana/alloy` | ships Docker + Hath logs → Loki |

Dev keeps a containerized stack (`./up` / `./down`). That path is Mac-only — not how the Linux box is operated.

## Two runtimes, one naming story

| Environment | Runtime | Definition |
| --- | --- | --- |
| Development | Docker Compose + Overmind on a Mac | `docker-compose.yml` + `Procfile` (this repo) |
| Production | bootc host systemd + podman modules | host units + quadlets under `/etc/containers/systemd/` |

Same `*.dadi` names. Prod: host is `os.dadi`; Caddy on the host. Dev: Tailscale sidecar + Compose DNS aliases.

**Prod updates:** `sudo bootc upgrade && sudo reboot` for nas/infra. Module images: `podman-auto-update` (no reboot). Hath: AppImage under cage, outside bootc/podman.

### First install

CD builds an unattended Anaconda ISO whenever `os/**` or `service/**` changes and publishes it on the `dadiOS-latest` GitHub Release (also `dadiOS-<sha>`).

1. Set repo secret `DADIOS_LUKS_PASSPHRASE` (required for ISO CD; no quotes, `#`, or backslashes).
2. Download `dadiOS-amd64.iso` from the `dadiOS-latest` release.
3. Flash it to a USB (Rufus, balenaEtcher, `dd`).
4. Boot the target machine from that USB. **The first disk is wiped with no confirmation** — unplug extra drives. Install is unattended: LUKS uses the CD secret, then reboots.
5. Remove the USB. At the LUKS prompt, enter the same passphrase. Console autologins as `ankur`. Hath starts under cage when the graphical target is up (downloads `hath-linux-x86_64.AppImage` on first start if missing).
6. SSH from a Mac that holds a private key matching [`os/authorized_keys`](os/authorized_keys): `ssh ankur@<box-ip>`.
7. Point a Cloudflare tunnel at this box’s Headscale (`localhost:8080`) for hostname `dadi.ardusa.dev`. Paste the tunnel token in Hath **System → MODULES → tunnel** (or `PUT /cloudflared/token` on nas).
8. Provision Hath clients against `https://dadi.ardusa.dev`. Verify `bootc status` and module health on System.

Machine config lives under `/var/lib/dadi/` (seeded on first boot from `/usr/share/dadi/seed`). Nas owns env/config files and stack lifecycle; Hath is only the UI.

Day-2 OS/infra updates: `sudo bootc upgrade && sudo reboot`. Module image updates: automatic via `podman-auto-update`. Hath: replace the AppImage under `/var/lib/dadi/hath/` (or delete it and restart `hath-kiosk` to re-fetch latest).

**Auth model:** LUKS unlocks the disk; SSH is key-only (`PasswordAuthentication no`); Hath has no login screen (mesh membership is the lock). Console is an appliance autologin, not a password prompt.

### Rollback

`sudo bootc rollback && sudo reboot`. The previous deployment is retained, so a bad image is always one reboot from reverted.

### Registry visibility

`nas` and module images are expected public so the box needs no pull credentials for those layers. Private module images would require `/etc/ostree/auth.json` / registry auth.

## Mesh

Headscale is the open-source **control plane**. The Tailscale **client** (`tailscaled` / tsnet) still joins the mesh — you are not using Tailscale Inc’s SaaS as authority.

Two naming layers exist at once and must not be confused:

- **Local / Compose DNS** — how processes on the Mac or containers reach each other in **dev** (Caddy `*.dadi` aliases). `curl http://yaad.dadi/health` via `/etc/hosts` → localhost:80.
- **Headscale MagicDNS** — how mesh clients (Hath) resolve `*.dadi`. Extra records point service names at the box’s mesh address (`os` / first allocation `100.64.0.1`). Host Caddy on `:80` routes by Host header.

Headscale must be reachable before a device joins. In **dev** that is `localhost:8080`. In **prod** clients use `https://dadi.ardusa.dev` (Cloudflare tunnel → Headscale on the box). `control_url` in the provisioning bundle carries that value.

**Prod:** host `tailscaled` joins as hostname `os`. **Dev:** a Tailscale sidecar container joins as `os` and L3-forwards to Caddy (`TS_EXPERIMENTAL_DEST_DNS_NAME=caddy`).

**Host / sidecar should be the first node.** MagicDNS extra records assume `100.64.0.1`. If anything else registers first, correct `dns.extra_records` and restart Headscale.

`bootstrap` is a oneshot: create user `ankur` if missing, mint a reusable mesh pre-auth key only if the authkey file is absent. Idempotent by design. Dev `./down --wipe` wipes mesh state volumes so the mesh rebuilds cleanly.

### Status errors in dev

`GET /status` returns `"errors": []` under Docker. Searchable errors live at `GET /logs`. Do not substitute `docker logs` into status — one honest empty list beats two diverging code paths.

## Logging

All modules emit **JSON lines on stdout** with at least `time`, `level`, `service`, and `msg`.

| Path | Role |
| --- | --- |
| Terminal | Overmind multiplexes Compose + Hath for live tails (dev) |
| Alloy | Prod: journald + Hath file → Loki; Dev: Docker + `nas/.run/hath.log` |
| Loki | Stores and indexes logs |
| `GET /logs` on nas | LogQL query API for Hath (filter by `services`, `level`, `q`, time range) |

Example:

```sh
curl -sG 'http://nas.dadi/logs' \
  --data-urlencode 'services=dwar,yaad' \
  --data-urlencode 'level=error' \
  --data-urlencode 'q=timeout'
```

`LOKI_URL` is required on nas (prod: `http://127.0.0.1:3100`; compose sets `http://loki:3100`). Hath sets `HATH_LOG_FILE` to `nas/.run/hath.log` when launched via `./up`. Each `./up` truncates that file and wipes the Loki volume so the explorer is a fresh session.

## One-time host setup (dev Mac)

**`/etc/hosts` is required.** Without it the stack looks broken — nothing on the host can resolve `.dadi` via Caddy. Add this line:

```
127.0.0.1  dwar.dadi yaad.dadi dimaag.dadi nas.dadi
```

`./up` checks for a `.dadi` line and exits with that exact entry if it is missing.

**Overmind** (and **tmux**) are required for the unified bring-up:

```sh
brew install overmind tmux
```

## Bring-up (dev)

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
| nas-service (dev) | bind mount + `air` |
| hath | Vite HMR / Tauri rebuild |

Start a subset when you only need containers:

```sh
docker compose up yaad yaad-postgres
```

## Not here yet

pause-other-modules-while-one-updates (prod + dev), TPM LUKS unlock, Hath logs widget polish / Plymouth splash branding.
