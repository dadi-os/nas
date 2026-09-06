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

One Docker network, `dadi`. Only Caddy publishes a host port. Caddy holds network aliases for `dwar.dadi`, `yaad.dadi`, `dimaag.dadi`, and `nas.dadi` so containers resolve those names the same way the Mac does via `/etc/hosts`.

## Two runtimes, one topology

| Environment | Runtime | Definition |
| --- | --- | --- |
| Development | Docker on a Mac | `docker-compose.yml` (this repo) |
| Production | podman + systemd on the box | quadlets in `/etc/containers/systemd/` (not yet) |

Same services, same names, same routing. Only the runtime differs. Only the dev half exists so far.

## One-time host setup

`.dadi` names must resolve on the Mac so Hath (a native app) and `curl` from the host can reach the stack. Add this line to `/etc/hosts`:

```
127.0.0.1  dwar.dadi yaad.dadi dimaag.dadi nas.dadi
```

Without it, nothing on the host can resolve `.dadi`. Include `nas.dadi` now even though nothing serves it yet.

## Bring-up

```sh
cp .env.example .env      # optional for bring-up; fill keys when you need providers
docker compose up --build
docker compose run --rm yaad npm run db:migrate
docker compose run --rm dimaag npm run db:migrate
```

Migrations are an explicit step — do not bake them into container startup. Dimaag's migrate also seeds root Dadi and syncs the tool registry.

Provider keys live in `.env` (gitignored). They are not required to start the stack — Dwar boots without them, logs which are missing, and returns `503 misconfigured` on routes that need a key. Database URLs, service addresses, and ports are topology and live in the compose file.

## Working on one module

Edit files in the sibling directory (`../yaad`, `../dimaag`, …). The bind mount and `tsx watch` pick up changes. Start a subset when you only need one service:

```sh
docker compose up yaad yaad-postgres
```

## Not here yet

Bootc image, quadlets, Headscale, LUKS, kiosk, and Nas's own HTTP API (`GET /status`, provisioning).
