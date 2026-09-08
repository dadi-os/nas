# Nas

Nas is the OS and infrastructure layer for dadi. It owns topology — which services exist, how they are networked and named, how they start, and how logs are collected and queried. It is the composition layer: the only place the full system is written down.

**One exported image:** `ghcr.io/dadi-os/nas` (bootc). Infra (Headscale, host Tailscale, Caddy, Loki, Alloy, control plane) is baked into that image and updates with `bootc upgrade` + reboot. The box UI is Plasma **bone glass** (Bloom field, translucent panels, દાદી brand, crest widgets, Preferences). Hath is for other devices only. App modules (`dwar`, `yaad`, `dimaag`) stay as containers and update via `podman-auto-update` with no reboot.

## Dependencies

Nas does not call other app modules as a client for its own control plane. It depends on:

- Headscale CLI on the host (device provisioning)
- Loki (`LOKI_URL`) for log query
- Host systemd / Docker Compose for stack lifecycle (`DADI_RUNTIME`)
- State directory for module env/config (`DADI_STATE_DIR`)

## Layout

```
nas/
  service/          Go control API (provision, status, logs, module config)
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
| `CONTROL_URL` | Headscale URL embedded in device bundles |
| `HEADSCALE_USER` | Headscale user for preauth keys |
| `LOKI_URL` | Loki base URL for `GET /logs` |
| `LISTEN_ADDR` | HTTP listen address |
| `DADI_STATE_DIR` | Persistent state root |
| `DADI_RUNTIME` | `podman` or `compose` |
| `DADI_COMPOSE_DIR` | Required when `DADI_RUNTIME=compose` |

Module secrets live in sibling repo `.env` files in compose (`../dwar/.env`, …) and under `DADI_STATE_DIR/modules/` on the appliance. Nas does not invent defaults for missing values.

## Local run

Dev is **headless Compose** on a Mac — no Plasma, no Tauri, no Overmind. The module stack plus browser Hath come up together; open `http://hath.dadi`.

One-time: `/etc/hosts` must resolve `*.dadi` (including `hath.dadi`) to localhost; Docker running; each sibling module has a `.env` (copy from `.env.example` if missing). Then:

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
| `service` | `nas` \| `dwar` \| `yaad` \| `dimaag` \| `hath` |
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
| `internal_error` | Unexpected server failure |

App modules may add domain-specific codes; they should reuse the table above for overlapping failures.

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
| `dwar` / `yaad` / `dimaag` (+ postgres / migrate) | podman quadlets | `AutoUpdate=registry`; `127.0.0.1:8081–8083` |

### Development (Mac Compose, headless)

| Service | Image source | Internal address |
| --- | --- | --- |
| `caddy` | `caddy:2-alpine` | host port 80; CORS for `Origin: http://hath.dadi` |
| `hath` | `../hath` `dev` target (Vite) | `*:8080` → `http://hath.dadi` |
| `dwar` / `yaad` / `dimaag` | sibling builds, `dev` target | `*:8080` |
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
6. Point a Cloudflare tunnel at Headscale; paste the token via Nas `PUT /cloudflared/token` (or Hath System → tunnel from another device).
7. Provision Hath clients: on the box open **Add Device** (dock / brand menu / Preferences → Devices), name the node, show the sage QR. Scan from Hath on the phone/laptop (`https://dadi.ardusa.dev` control URL is embedded in the bundle).

Day-2: `sudo bootc upgrade && sudo reboot` for nas/infra; module images via `podman-auto-update`. Rollback: `sudo bootc rollback && sudo reboot`.

## Desktop (dadiOS)

Plasma on the box only — Hath is for other devices. Visual system is **bone glass** (aligned with Hath `src/styles/tokens.css`): light field, frosted veil panels, sage accent, brand mark **દાદી** only (no Latin “DADI” / “OS” in chrome).

| Layer | Spec |
| --- | --- |
| Field | Wallpapers `DadiBloom` (default), `DadiMist`, `DadiVein` |
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
| Preferences | `dadi-preferences` → `plasmawindowed org.dadi.preferences` (module `.env` / dwar config / tunnel / devices → `DADI_STATE_DIR`) |
| Add Device | `dadi-add-device` → `plasmawindowed org.dadi.adddevice` (mint Nas `POST /provision` QR for Hath) |
| Wallpaper | `dadi-wallpaper Bloom\|Mist\|Vein` |
| Blur / lid | `os/etc/xdg/kwinrc`, `os/etc/systemd/logind.conf.d/dadi-lid.conf` |
| Plymouth / SDDM | theme `dadi` |

Glass rules: blur before tint; never translucent text; two opacities only (veil / sheet); no dark glass.

## Mesh

Headscale is the control plane; Tailscale clients join the mesh. Dev Headscale is `localhost:8080`; prod clients use `https://dadi.ardusa.dev`. Host/sidecar hostname `os` should be the first node so MagicDNS extra records match `100.64.0.1`.

`GET /status` returns `"errors": []` under Docker — searchable errors live at `GET /logs`.

## One-time host setup (dev Mac)

Add to `/etc/hosts`:

```
127.0.0.1  dwar.dadi yaad.dadi dimaag.dadi nas.dadi hath.dadi
```

Copy module env files if missing:

```sh
cp ../dwar/.env.example ../dwar/.env
cp ../yaad/.env.example ../yaad/.env
cp ../dimaag/.env.example ../dimaag/.env
```

Docker Desktop (or equivalent) must be running. No Overmind / tmux.

## Working on one module

Edit sibling directories (`../yaad`, …). Bind mounts + watchers pick up changes. Start a subset with `docker compose up yaad yaad-postgres` when you only need those containers. For UI-only work: `docker compose up hath caddy nas-service …` or just `docker compose up --build` and open `http://hath.dadi`.
