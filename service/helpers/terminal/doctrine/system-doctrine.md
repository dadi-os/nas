# System Doctrine

## Core principle

Systems fail loudly and early. A missing config, broken API call, or invalid state should stop — not degrade into a fake success path.

## No fallback systems

This is a hard rule. Do not implement:

| Anti-pattern | Example | Do instead |
|---|---|---|
| Silent error swallowing | `.catch(() => {})`, empty `catch {}` | Let the error surface or handle it explicitly with user-visible feedback |
| Fake placeholder data | `username ?? 'ardusa'` when auth is missing | Require the real value; throw or block rendering |
| Env var defaults in code | `VITE_API_URL ?? 'http://localhost:8080'` | Require the env var; document in `.env.example` |
| Cascading error messages | try A, then B, then generic fallback string | One error path; propagate the server message |
| Failed fetch → empty state | mechanics fetch fails → show `[]` | Show the error; do not pretend success |
| Auto-create missing infra | ensure queue exists if URL missing | Require explicit config; fail at startup |
| try/catch dynamic import | import fallback when module missing | Fix the import path; fail at build time |
| Default game/theme/color | `game ?? 'rl'` when game not selected | Use neutral styling until selection, or require selection before render |
| Soft empty config | `DATABASE_URL ?? ""` then fail later | Fail at config load / process start |

## What is NOT a fallback

These are fine:

- **Null coalescing for optional URL params** (`searchParams.get('q') || ''`) — the param is genuinely optional
- **Optional function parameters** with defaults (`className = ''`)
- **Object initialization** (`groups[type] ??= []`) — building a map, not masking failure
- **Display coalescing for optional contract fields** (`name ?? "untitled"`) when the field is legitimately optional in the API
- **Lazy loading / code splitting** — deliberate load strategy, not error recovery (prefer not to use as recovery)
- **Explicit user-facing defaults** documented as product decisions (not silent code defaults)
- **Bounded logged retries** for transient network failures — resilience, not pretending success

## Configuration

- All service URLs, credentials, and connection strings come from environment variables (or explicit constants when mesh topology is fixed by design — still no localhost env fallbacks).
- Application code does not embed defaults for external services.
- `.env.example` lists every required variable.
- Startup fails immediately if required config is missing.
- Docker Compose / deployment manifests set the variables explicitly.

## Errors

- API clients extract server error messages and throw them — no layered fallback message chains.
- Prefer stable machine codes: `{ error: { type: "<code>", message: "..." } }` where `type` is a catalogued code.
- UI shows error states to the user; does not silently substitute empty data.
- Backend returns explicit error JSON; does not return partial success with missing fields.

## Auth

- Authenticated routes assume a valid authenticated user.
- Do not render authenticated UI with hardcoded placeholder identities.
- Missing claims (username, sub) are errors, not occasions for fake data.

## Data integrity

- Foreign keys reference the correct entity (user UUID, not Keycloak sub string).
- Validate at the boundary; do not assume upstream got it right.

## Media and uploads

- Presigned URL upload failures reject the promise — no silent retry loops without visibility.
- Progress and error states are shown in the UI during upload.

## Observability

- Structured logs with stable fields (`time`, `level`, `service`, `msg`; optional `code`, `request_id`, request summary).
- One meaningful request summary line beats framework access spam.
- Drop collector noise (npm banners, deprecation dumps, DB NOTICE pretty-prints) at the ship path when possible.
- Module down / config missing / upstream failure should be obvious in logs and status.

## Resilience vs fallbacks

- Retries with backoff for transient network failures are acceptable when explicit, logged, and bounded — not silent infinite loops.
- Do not conflate resilience (retry a flaky receive) with fallbacks (pretend data loaded when it did not).
