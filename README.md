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
| Production | podman + systemd on the box | quadlets in `/etc/containers/systemd/`; `podman-auto-update` for module images |

Same services, same names, same routing. Only the runtime differs.

**Prod logging:** Alloy reads journald (podman/systemd) into the same Loki shape; `GET /logs` stays the client API. `podman-auto-update` restarts updated units; log identity is the module/unit name.

## Production

**Two layers, two update mechanisms.** The OS layer is a bootc image (`ghcr.io/dadi-os/nas-os`). Update it with `bootc upgrade` and a reboot; revert with `bootc rollback`. The module layer is ordinary container images (`ghcr.io/dadi-os/{dwar,yaad,dimaag,nas-service}`). Those update via `podman-auto-update` with no reboot — `AutoUpdate=registry` on each quadlet plus `podman-auto-update.timer`. Module images are never baked into the OS image. Hath is a third path (AppImage under cage), updated outside bootc/podman.

### First install

CD builds an unattended Anaconda ISO whenever `os/**` changes and publishes it on the `dadiOS-latest` GitHub Release (also `dadiOS-<sha>`).

1. Set repo secret `DADIOS_LUKS_PASSPHRASE` (required for ISO CD; no quotes, `#`, or backslashes).
2. Download `dadiOS-amd64.iso` from the `dadiOS-latest` release.
3. Flash it to a USB (Rufus, balenaEtcher, `dd`).
4. Boot the target machine from that USB. **The first disk is wiped with no confirmation** — unplug extra drives. Install is unattended: LUKS uses the CD secret, then reboots.
5. Remove the USB. At the LUKS prompt, enter the same passphrase. Console autologins as `ankur`. Hath starts under cage when the graphical target is up (downloads `hath-linux-x86_64.AppImage` on first start if missing).
6. SSH from a Mac that holds a private key matching [`os/authorized_keys`](os/authorized_keys): `ssh ankur@<box-ip>`.
7. Point a Cloudflare tunnel at this box’s Headscale (`localhost:8080`) for hostname `headscale.dadi.ardusa.dev`. Paste the tunnel token in Hath **System → MODULES → tunnel** (or `PUT /cloudflared/token` on nas-service).
8. Provision Hath clients against `https://headscale.dadi.ardusa.dev`. Verify `bootc status` and module health on System.

Machine config lives under `/var/lib/dadi/` (seeded on first boot from `/usr/share/dadi/seed`). Nas owns env/config files and stack lifecycle; Hath is only the UI.

Day-2 OS updates: `sudo bootc upgrade && sudo reboot`. Module image updates: automatic via `podman-auto-update` (selective — only changed images). Hath: replace the AppImage under `/var/lib/dadi/hath/` (or delete it and restart `hath-kiosk` to re-fetch latest).

**Auth model:** LUKS unlocks the disk; SSH is key-only (`PasswordAuthentication no`); Hath has no login screen (mesh membership is the lock). Console is an appliance autologin, not a password prompt.

### Rollback

`sudo bootc rollback && sudo reboot`. The previous deployment is retained, so a bad image is always one reboot from reverted.

### Registry visibility

`nas-os` and module images are expected public so the box needs no pull credentials for those layers. Private module images would require `/etc/ostree/auth.json` / registry auth.
## Mesh

Two naming layers exist at once and must not be confused:

- **Docker DNS** — how containers reach each other. Caddy's `*.dadi` network aliases are this layer. `curl http://yaad.dadi/health` from the Mac via `/etc/hosts` → localhost:80 is still this path.
- **Headscale MagicDNS** — how tsnet clients (Hath) resolve `*.dadi`. Extra records in `headscale/config.yaml` (dev) / `/etc/headscale/config.yaml` (prod) point service names at the sidecar's mesh address. Separate namespace, separate mechanism. Neither replaces the other.

Headscale must be reachable before a device joins the mesh. In **dev** that is `localhost:8080`. In **prod** clients use `https://headscale.dadi.ardusa.dev` (Cloudflare tunnel → Headscale on the box). `control_url` in the provisioning bundle carries that value.

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

`LOKI_URL` is required on nas-service (compose sets `http://loki:3100`). Hath sets `HATH_LOG_FILE` to `nas/.run/hath.log` when launched via `./up`. Each `./up` truncates that file and wipes the Loki volume so the explorer is a fresh session.

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

pause-other-modules-while-one-updates (prod + dev), TPM LUKS unlock, Hath logs widget polish / Plymouth splash branding.
