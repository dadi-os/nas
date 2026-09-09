# Nas

Nas is the OS and infrastructure layer for dadi. It owns topology — which services exist, how they are networked and named, how they start, and how logs are collected and queried. It is the composition layer: the only place the full system is written down.

**One exported image:** `ghcr.io/dadi-os/nas` (bootc). Infra (Headscale, host Tailscale, Caddy, Loki, Alloy, control plane) is baked into that image and updates with `bootc upgrade` + reboot. The box UI is Plasma **bone glass** (leaf field, translucent panels, દાદી brand, crest widgets, Preferences). Hath is for other devices only. App modules (`dwar`, `yaad`, `dimaag`, `ghar`) stay as containers and update via `podman-auto-update` with no reboot.

## Dependencies

Nas does not call other app modules as a client for its own control plane. It depends on:

- Headscale CLI on the host (device provisioning)
- Loki (`LOKI_URL`) for log query
- Host systemd / Docker Compose for stack lifecycle (`DADI_RUNTIME`)
- State directory for module env/config (`DADI_STATE_DIR`)
- Host `tmux` (terminals) and `rg` (filesystem grep); appliance also needs `runuser` from util-linux
- Host `Xvfb`, Chromium, and ImageMagick `import` (browsers); fonts for page text

## Layout

```
nas/
  service/          Go control API (provision, status, logs, terminals, fs, browsers, module config)
  logging/          Dev Alloy + Loki configs
  os/               bootc image, host units, Plasma desktop, prod Alloy, installer
  headscale/        Headscale config templates
  docker-compose.yml
```

Desktop assets live under `os/usr/share/` (look-and-feel, plasmoids, wallpapers, Preferences) and `os/etc/xdg/` (colors, kwin blur). Brand SVGs: `os/usr/share/dadi/brand/`.

## Config vs env

Required on the control service (fail at startup if missing):

| Variable | Meaning |
| --- | --- |
| `HEADSCALE_USER` | Headscale user for preauth keys |
| `LOKI_URL` | Loki base URL for `GET /logs` |
| `LISTEN_ADDR` | HTTP listen address |
| `DADI_STATE_DIR` | Persistent state root |
| `DADI_RUNTIME` | `podman` or `compose` |
| `DADI_COMPOSE_DIR` | Required when `DADI_RUNTIME=compose` |

Optional seed (written once into `/var/lib/dadi/headscale/control_url` when that file is empty):

| Variable | Meaning |
| --- | --- |
| `CONTROL_URL` | Initial public Headscale URL for device provision bundles |

After seed, Preferences → Tunnel (or `PUT /headscale/control-url`) is the sole source of truth. Provision fails until the file is non-empty.

Module secrets live in sibling `../dwar/.env` in compose and under `DADI_STATE_DIR/modules/dwar/` on the appliance. Yaad/Dimaag Postgres credentials are baked into `docker-compose.yml` and the podman quadlets — not user `.env` files. Nas does not invent defaults for missing Dwar values.

## Local run

Dev is **headless Compose** on a Mac — no Plasma, no Tauri, no Overmind. The module stack plus browser Hath come up together; open `http://hath.dadi`.

One-time: `/etc/hosts` must resolve `*.dadi` (including `hath.dadi`) to localhost; Docker running; `../dwar/.env` present (copy from `.env.example` if missing). Then:

```sh
docker compose up --build
# → http://hath.dadi

docker compose down       # stop containers
docker compose down -v    # also destroy volumes
```

Tauri Hath (mesh / provisioning work) is separate: `cd ../hath && net/build.sh && npm run tauri dev`.

## CI / CD

| Workflow | When | What |
| --- | --- | --- |
| `ci.yml` | PR + push to `main` | `go test` / `go vet` in `service/` |
| `cd.yml` | `os/**` or `service/**` on `main` (or dispatch) | Build/push `nas` image; build LUKS installer ISO; publish `dadiOS-*` releases |

Concurrency cancels superseded CI runs on the same ref.

## Logging contract (source of truth)

All dadi modules emit **one JSON object per line** on stdout. Dev Alloy scrapes Docker container logs only (no Hath file tail — browser Hath logs stay in the browser console).

| Field | Meaning |
| --- | --- |
| `time` | RFC3339 / RFC3339Nano UTC |
| `level` | `debug` \| `info` \| `warn` \| `error` |
| `service` | `nas` \| `dwar` \| `yaad` \| `dimaag` \| `hath` \| `ghar` |
| `msg` | Human message; may include `\n` for multi-line detail |
| `code` | Stable error/event code when applicable |
| `request_id` | Per-request correlation id |
| `method`, `path`, `status`, `duration_ms` | HTTP request summary (one line per request) |

Alloy drops non-JSON lines for app services and drops infra noise (postgres, headscale, alloy, loki, caddy, hath Vite, …). Do not emit npm/tsx banners, uvicorn access spam, or Fastify boot chatter as the primary signal.

### Shared error codes

HTTP errors use `{ "error": { "type": "<code>", "message": "..." } }` where `type` equals the code. Infra / cross-cutting codes (reuse across modules):

| Code | Meaning |
| --- | --- |
| `config_missing` | Required env/config absent at boot |
| `upstream_unreachable` | Downstream HTTP/TCP unreachable |
| `upstream_timeout` | Downstream timed out |
| `provider_unconfigured` | Inference provider key missing (dwar) |
| `module_unhealthy` | Module health check failed |
| `provision_failed` | Device provision / Headscale key mint failed |
| `log_query_failed` | Loki query or parse failed |
| `invalid_request` | Bad client input |
| `not_found` | Unknown resource |
| `forbidden` | Write into OS / dadiOS runtime paths (see Host agent) |
| `busy` | Terminal pane already running a command / in-flight exec |
| `binary_file` | Filesystem read of a binary file (415) |
| `conflict` | FS edit matched 0 or many times (need exactly one) |
| `internal_error` | Unexpected server failure |

App modules may add domain-specific codes; they should reuse the table above for overlapping failures.

## Host agent surface (terminals + filesystem)

Anonymous HTTP on `LISTEN_ADDR`. Nothing is persisted in Nas — **tmux is the registry of terminals**. Every terminal endpoint re-validates the target with tmux at call time.

### Host user

| Piece | Value |
| --- | --- |
| System user | `dadi` (created in the OS Containerfile; home `$DADI_STATE_DIR`; shell `/bin/bash`) |
| tmux socket | `/run/dadi/tmux.sock` on appliance (`tmpfiles.d`); under `$DADI_STATE_DIR/run` in Compose |
| Default cwd | `$DADI_STATE_DIR` when terminal / glob / grep omit `cwd` |

On the appliance (`DADI_RUNTIME=podman`) Nas runs as root and launches every tmux command via `runuser -u dadi --` with `-S /run/dadi/tmux.sock`. In Compose/dev the process already runs as the container user, so the user switch is skipped; the same socket flag and code path remain.

There is **no project sandbox folder**. Agents may read any absolute path. **Writes** are denied under OS and dadiOS runtime trees (symlinks resolved before the check): `/usr`, `/boot`, `/etc`, `/lib`, `/lib64`, `/bin`, `/sbin`, `/root`, `/var/lib/containers`, and under `$DADI_STATE_DIR`: `modules`, `cloudflared`, `headscale`, `browsers`, `run`. Darwin also denies `/System` and `/Library`. Terminals are not path-jailed — the FS API is the write gate; shell power is bounded by the `dadi` OS user.

### Terminals

Session names are `t<n>` for positive integers. `POST /terminals` picks the lowest `n` absent from `tmux ls`. History limit is `50000`.

| Method | Path | Body / query | Success | Errors |
| --- | --- | --- | --- | --- |
| `POST` | `/terminals` | `{ "cwd"?: string }` (default state dir) | `{ id, cwd }` | `invalid_request` |
| `GET` | `/terminals` | — | `[{ id, cwd, created_at, busy }]` | — |
| `POST` | `/terminals/{id}/exec` | `{ "command": string, "timeout_seconds"?: number (default 120, max 3600), "max_bytes"?: number (default 32768) }` | `{ exit_code, output, truncated, timed_out }` | `not_found`, `busy` (409), `invalid_request` |
| `GET` | `/terminals/{id}/capture` | `?lines=N` (default 200) | `{ output }` | `not_found`, `invalid_request` |
| `POST` | `/terminals/{id}/keys` | `{ "keys": string[] }` (verbatim `tmux send-keys`, e.g. `["C-c"]`) | `{ sent: true }` | `not_found`, `invalid_request` |
| `DELETE` | `/terminals/{id}` | — | 204 | `not_found` |

`busy` is true when `#{pane_current_command}` is not the login shell, or an exec is in flight. Exec appends a nonce marker after the command, polls `capture-pane` every 200ms, and returns output between the echoed command and the marker. On timeout the command keeps running (`timed_out: true`, `exit_code: null`); use `capture` / `keys` to follow up. Truncation keeps head and tail with an elided-bytes note in the middle.

### Filesystem

Paths must be absolute. Reads are unrestricted (aside from `binary_file`). Writes/edits fail with `forbidden` on protected prefixes above. Writes chown to `dadi` on the appliance.

| Method | Path | Body | Success | Errors |
| --- | --- | --- | --- | --- |
| `POST` | `/fs/read` | `{ path, offset?: number (1-based), limit?: number (default 500), max_bytes?: number (default 65536) }` | `{ content, total_lines, truncated }` (`N\tline`) | `not_found`, `binary_file` (415), `invalid_request` |
| `POST` | `/fs/write` | `{ path, content }` (creates parents) | `{ bytes }` | `forbidden`, `invalid_request` |
| `POST` | `/fs/edit` | `{ path, old_string, new_string }` (exactly one match) | `{ replaced: true }` | `forbidden`, `not_found`, `conflict` (409) |
| `POST` | `/fs/glob` | `{ pattern, cwd?, limit?: number (default 500) }` | `{ paths, truncated }` (mtime desc) | `not_found`, `invalid_request` |
| `POST` | `/fs/grep` | `{ pattern, cwd?, glob?, limit?: number (default 200), max_bytes?: number (default 65536) }` | `{ matches: [{ path, line, text }], truncated }` | `not_found`, `invalid_request` |

### Browsers

Each browser is a headed Chromium on its own Xvfb display (not headless, not a VM). Nas only spawns, lists, kills, proxies CDP, and screenshots the virtual monitor. Callers drive pages over CDP. Running processes are the registry — nothing is stored in Nas.

**Derivation from integer id `n` (lowest free `n >= 10`; 1–9 reserved for Plasma):**

| Field | Value |
| --- | --- |
| X display | `:n` |
| CDP port | `9300 + n` (loopback only) |
| Profile dir | `$DADI_STATE_DIR/browsers/<n>` — created on first spawn, **never deleted by Nas** (cookies/logins survive kill + reboot when the id is reused) |

`create` picks the lowest `n >= 10` whose `/tmp/.X11-unix/X<n>` is absent and whose CDP port is free. Spawns Xvfb as root (so it can bind `/tmp/.X11-unix`), then Chromium as `dadi` when that user exists — Chromium refuses to run as root without `--no-sandbox`. Window size 1920×1080; profile under `--user-data-dir`. On the appliance (`DADI_RUNTIME=podman`) the Chromium sandbox stays on. Compose/dev Docker disables user namespaces, so Chromium gets `--no-sandbox` and `--disable-dev-shm-usage` only in that runtime. Both processes use `setsid` so a Nas restart does not take them down. Stderr is logged under the `nas` service with `browser=<n>`.

| Method | Path | Success | Errors |
| --- | --- | --- | --- |
| `POST` | `/browsers` | `{ id, display, cdp_url }` | `internal_error` (Xvfb/Chromium startup; message includes stderr) |
| `GET` | `/browsers` | `[{ id, display, cdp_url, healthy }]` — `healthy` false if Xvfb is up but CDP is not | — |
| `DELETE` | `/browsers/{id}` | 204 (SIGTERM process groups, wait ≤5s, SIGKILL) | `not_found` if neither process exists |
| `GET` | `/browsers/{id}/json/version` | Chromium `/json/version` with `ws://127.0.0.1:<port>/…` rewritten to `ws://<Host>/browsers/<id>/…` | `not_found`, `upstream_unreachable` |
| `GET` | `/browsers/{id}/json/list` | Same rewrite for `/json/list` | `not_found`, `upstream_unreachable` |
| `GET` | `/browsers/{id}/devtools/{rest…}` | WebSocket reverse proxy to `ws://127.0.0.1:<port>/devtools/{rest}` (no buffering / idle timeout on the upgrade) | `not_found`, `upstream_unreachable` |
| `GET` | `/browsers/{id}/screenshot` | `image/png` of the whole virtual monitor (`import -display :n -window root`) | `not_found`, `internal_error` |

`cdp_url` is `ws://<request Host>/browsers/<id>/devtools/browser/<uuid>` from `/json/version` after rewrite — hand it straight to a CDP client. Screenshot is the only way to see popups, download bars, and chrome outside the page; page screenshots stay on CDP.

Chromium binary defaults to `chromium-browser` (Fedora). Set `CHROMIUM_BIN` (Compose/dev image sets `chromium`).

Caddy `http://nas.dadi` is a plain `reverse_proxy` to Nas (`127.0.0.1:8092` in prod, `nas-service:8080` in Compose). Caddy proxies WebSocket upgrades by default — no extra config required for CDP.

Log query example:

```sh
curl -sG 'http://nas.dadi/logs' \
  --data-urlencode 'services=dwar,yaad' \
  --data-urlencode 'level=error' \
  --data-urlencode 'q=timeout'
```

## Topology

### Production (host + app containers)

| Unit | Kind | Notes |
| --- | --- | --- |
| `headscale` | host systemd | control plane; `:8080` |
| `bootstrap` | host oneshot | Headscale user + host mesh auth key |
| `tailscaled` + `dadi-tailscale` | host | mesh node `os` → MagicDNS `os.dadi` |
| `caddy` | host systemd | `:80`, Host-header → localhost app ports |
| `nas` | host systemd | control API on `127.0.0.1:8092` |
| `loki` / `alloy` | host systemd | logs |
| `cloudflared` | host systemd | tunnel to Headscale |
| `sddm` + Plasma | host graphical | bone glass desktop; દાદી brand; Preferences + crest widgets |
| `dwar` / `yaad` / `dimaag` / `ghar` (+ postgres / migrate) | podman quadlets | `AutoUpdate=registry`; `127.0.0.1:8081–8084` (`ghar` uses `Network=host`, binds loopback) |

### Development (Mac Compose, headless)

| Service | Image source | Internal address |
| --- | --- | --- |
| `caddy` | `caddy:2-alpine` | host port 80; CORS for `Origin: http://hath.dadi` |
| `hath` | `../hath` `dev` target (Vite) | `*:8080` → `http://hath.dadi` |
| `dwar` / `yaad` / `dimaag` / `ghar` | sibling builds, `dev` target | `*:8080` (Matter does not work on Mac Docker) |
| `nas-service` | `./service` | host `8092` |
| `loki` / `alloy` | official images | log pipeline |
| `headscale` / `tailscale` | official images | mesh |

| Environment | Runtime | Definition |
| --- | --- | --- |
| Development | Docker Compose on a Mac (headless) | `docker-compose.yml` |
| Production | bootc host systemd + podman modules | units + quadlets under `/etc/containers/systemd/` |

Same `*.dadi` names in both environments. Dev does not run Plasma; the UI under test is browser Hath.

### First install

CD builds an unattended Anaconda ISO whenever `os/**` or `service/**` changes and publishes it on the `dadiOS-latest` GitHub Release (also `dadiOS-<sha>`).

1. Set repo secret `DADIOS_LUKS_PASSPHRASE` (no quotes, `#`, or backslashes).
2. Download all `dadiOS-amd64.iso.*` parts from the `dadiOS-latest` release and reassemble: `cat dadiOS-amd64.iso.* > dadiOS-amd64.iso`.
3. Flash to USB; boot the target machine. **The first disk is wiped with no confirmation.**
4. At the LUKS prompt, enter the passphrase. SDDM autologins as `ankur` into Plasma (દાદી desktop).
5. SSH with a key matching [`os/authorized_keys`](os/authorized_keys).
6. Point a Cloudflare tunnel at Headscale; set the public control plane URL and paste the tunnel token via Nas Preferences → Tunnel (`PUT /headscale/control-url`, `PUT /cloudflared/token`) or Hath System → tunnel from another device.
7. Provision Hath clients: on the box open **Add Device** (dock / brand menu / Preferences → Devices), name the node, show the sage QR. Scan from Hath on the phone/laptop (`https://dadi.ardusa.dev` control URL is embedded in the bundle).

Day-2: `sudo bootc upgrade && sudo reboot` for nas/infra; module images via `podman-auto-update`. Rollback: `sudo bootc rollback && sudo reboot`.

### Host firewall (nftables)

Ruleset: `/etc/nftables/dadi.nft` (loaded by `nftables.service`). Default-deny input except loopback, Tailscale (`tailscale0`), SSH, and Matter on the LAN (UDP 5353 / 5540 + IPv6 multicast). ICMPv6 is accepted so neighbor discovery works. Ghar's HTTP port `8084` is explicitly dropped off-loopback; the process also binds `127.0.0.1` only.

## Desktop (dadiOS)

Plasma on the box only — Hath is for other devices. Visual system is **bone glass** (aligned with Hath `src/styles/tokens.css`): light field, frosted veil panels, sage accent, brand mark **દાદી** only (no Latin “DADI” / “OS” in chrome).

| Layer | Spec |
| --- | --- |
| Field | Wallpaper `Dadi` — sage leaf + glass leaf on bone |
| Veil | Menu bar / dock / widgets — ~62% bone + blur 32px + specular rim |
| Sheet | Popovers — ~78% bone + blur 24px |
| Solid | Editors / Preferences content — `#fafaf7` |
| Ink | Type and icons always opaque |

| Token | Value |
| --- | --- |
| Bone / raised | `#fafaf7` / `#f7f9f4` |
| Sage / text / deep / line | `#8fa382` / `#7e9270` / `#5c6b52` / `#b9c9ab` |
| Ink / muted | `#2c302a` / `#6e7568` |
| Radii | window 12 · control 9 · dock 24 |

| Surface | Where |
| --- | --- |
| Look-and-feel | `org.dadi.desktop` — translucent top bar (32px), autohide float dock, crest widgets |
| Brand menu | plasmoid `org.dadi.brand` |
| Widgets | `org.dadi.widget.{system,memory,agents,timeline,logs}` |
| Preferences | `dadi-preferences` → `plasmawindowed org.dadi.preferences` (dwar `.env` / config / tunnel / devices → `DADI_STATE_DIR`) |
| Add Device | `dadi-add-device` → `plasmawindowed org.dadi.adddevice` (mint Nas `POST /provision` QR for Hath) |
| Wallpaper | `Dadi` (`/usr/share/wallpapers/Dadi/`) |
| Blur / lid | `os/etc/xdg/kwinrc`, `os/etc/systemd/logind.conf.d/dadi-lid.conf` |
| Plymouth / SDDM | theme `dadi` |

Glass rules: blur before tint; never translucent text; two opacities only (veil / sheet); no dark glass.

## Mesh

Headscale is the control plane; Tailscale clients join the mesh. Dev Headscale is `localhost:8080`; prod clients use `https://dadi.ardusa.dev`. Host/sidecar hostname `os` should be the first node so MagicDNS extra records match `100.64.0.1`.

`GET /status` returns `"errors": []` under Docker — searchable errors live at `GET /logs`.

## One-time host setup (dev Mac)

Add to `/etc/hosts`:

```
127.0.0.1  dwar.dadi yaad.dadi dimaag.dadi ghar.dadi nas.dadi hath.dadi
```

Copy Dwar env if missing:

```sh
cp ../dwar/.env.example ../dwar/.env
```

Yaad, Dimaag, and Ghar need no `.env` — Nas injects fixed local Postgres credentials.

Docker Desktop (or equivalent) must be running. No Overmind / tmux.

## Working on one module

Edit sibling directories (`../yaad`, …). Bind mounts + watchers pick up changes. Start a subset with `docker compose up yaad yaad-postgres` when you only need those containers. For UI-only work: `docker compose up hath caddy nas-service …` or just `docker compose up --build` and open `http://hath.dadi`.
