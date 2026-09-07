# Nas

Nas is the OS and infrastructure layer. It owns the topology — what services exist, how they are networked, how names route, and how they start. Every other module describes only what it depends on; Nas is the composition layer, so it is the only place the full system is written down.

## Topology

| Service | Image source | Internal address |
| --- | --- | --- |
| `caddy` | `caddy:2-alpine` | publishes host port 80 |
| `dwar` | build `../dwar` | `dwar:8080` |
| `yaad` | build `../yaad`, target `dev` | `yaad:8080` |
| `yaad-postgres` | `pgvector/pgvector:pg18` | `yaad-postgres:5432` |
| `dimaag` | build `../dimaag`, target `dev` | `dimaag:8080` |
| `dimaag-postgres` | `postgres:18` | `dimaag-postgres:5432` |
| `headscale` | `headscale/headscale:0.26` | publishes host port 8080 |
| `tailscale` | `tailscale/tailscale:latest` | mesh node `os` → forwards to Caddy |
| `nas-service` | build `./service` | `nas-service:8080`, host `8092` for provisioning |
| `bootstrap` | oneshot from `./service` | creates Headscale user + sidecar auth key |

One Docker network, `dadi`. Caddy publishes host port 80. Headscale publishes host port 8080 (see Mesh). Nas's HTTP API is published on host port 8092 only so Mac-side provisioning can curl it before the mesh is up; all other access goes through Caddy. Caddy holds network aliases for `dwar.dadi`, `yaad.dadi`, `dimaag.dadi`, and `nas.dadi` so containers resolve those names the same way the Mac does via `/etc/hosts`.

## Two runtimes, one topology

| Environment | Runtime | Definition |
| --- | --- | --- |
| Development | Docker on a Mac | `docker-compose.yml` (this repo) |
| Production | podman + systemd on the box | quadlets in `/etc/containers/systemd/` (not yet) |

Same services, same names, same routing. Only the runtime differs. Only the dev half exists so far.

## Mesh

Two naming layers exist at once and must not be confused:

- **Docker DNS** — how containers reach each other. Caddy's `*.dadi` network aliases are this layer. `curl http://yaad.dadi/health` from the Mac via `/etc/hosts` → localhost:80 is still this path.
- **Headscale MagicDNS** — how tsnet clients (Hath) resolve `*.dadi`. Extra records in `headscale/config.yaml` point service names at the sidecar's mesh address. Separate namespace, separate mechanism. Neither replaces the other.

Headscale is the one exception to "only Caddy publishes a host port." A device that has not joined the mesh cannot resolve `.dadi` names, so the control server must be reachable by ordinary means (`localhost:8080` in dev). That is also why `control_url` lives in the provisioning bundle rather than as a constant — production swaps the address without a code change.

The Tailscale sidecar joins as hostname `os` (MagicDNS: `os.dadi`) and L3-forwards inbound mesh traffic to Caddy, which routes by Host header. Current Tailscale rejects `TS_DEST_IP` together with userspace mode, so the sidecar runs with kernel networking (`NET_ADMIN` + `/dev/net/tun`) and `TS_EXPERIMENTAL_DEST_DNS_NAME=caddy`.

**Sidecar must be the first node.** MagicDNS extra records assume the sidecar receives `100.64.0.1` (Headscale's first sequential allocation). If anything else registers first, those records are wrong — run `docker compose exec headscale headscale nodes list -o json`, put the sidecar's real address into `headscale/config.yaml` `dns.extra_records`, and restart Headscale. Prefer keeping the sidecar first over automating around the assumption.

`bootstrap` is a oneshot that runs on every `docker compose up`: create user `ankur` if missing, mint a reusable sidecar pre-auth key only if `/run/bootstrap/authkey` is absent. Idempotent by design. `docker compose down -v` wipes `headscale_data`, `tailscale_state`, and `bootstrap`, and the whole mesh rebuilds cleanly.

### Manual provisioning (dev Mac)

Hath runs on the host, outside Docker. Mint a one-shot bundle and paste it into Hath's setup screen (once per `down -v`):

```sh
curl -s -X POST http://localhost:8092/provision \
  -H 'content-type: application/json' \
  -d '{"node_name":"ankur-macbook"}' | jq -r .bundle
```

On the box, Nas will write the credentials file into Hath's app data before the kiosk starts so the setup screen never appears. That writer is a later phase; Hath already loads a pre-existing `credentials.json` and connects without prompting.

### Status errors in dev

`GET /status` returns `"errors": []` under Docker. That is correct — error logs come from journald, which does not exist here. Do not substitute `docker logs`; one honest empty list beats two diverging code paths.

## One-time host setup

`.dadi` names must resolve on the Mac so Hath (a native app) and `curl` from the host can reach the stack. Add this line to `/etc/hosts`:

```
127.0.0.1  dwar.dadi yaad.dadi dimaag.dadi nas.dadi
```

Without it, nothing on the host can resolve `.dadi` via the Caddy path.

## Bring-up

```sh
cp ../dwar/.env.example ../dwar/.env       # may stay empty if you do not need model calls
cp ../yaad/.env.example ../yaad/.env
cp ../dimaag/.env.example ../dimaag/.env
docker compose up --build
docker compose run --rm yaad npm run db:migrate
docker compose run --rm dimaag npm run db:migrate
```

Migrations are an explicit step — do not bake them into container startup. Dimaag's migrate also seeds root Dadi and syncs the tool registry.

Each module owns its `.env` (gitignored). Nas points `env_file` at those files. Dwar's keys may be left empty for a stack that does not need model calls — Dwar boots and returns `503 provider_unconfigured` on routes that need a key. Database passwords and URLs live in each module's `.env`; `POSTGRES_USER` / `POSTGRES_DB` stay inline in compose as topology.

## Working on one module

Edit files in the sibling directory (`../yaad`, `../dimaag`, …). The bind mount and `tsx watch` pick up changes. Start a subset when you only need one service:

```sh
docker compose up yaad yaad-postgres
```

## Not here yet

Bootc image, quadlets, LUKS, kiosk auto-provisioning writer, and remote (off-LAN) mesh join.
