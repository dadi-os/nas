# Coding Doctrine

## Core principle

Write the simplest correct code. Every line should earn its place.

## Scope

- Make the smallest diff that solves the problem.
- Do not touch unrelated code, dead code cleanup, or drive-by refactors unless asked.
- A focused five-line fix beats a hundred-line abstraction.

## Abstraction

- Do not over-engineer. No premature layers (service/repository/manager) when a handler or function suffices.
- Do not extract one- or two-line helpers that should stay inline.
- Do not create wrapper components or files that only re-export or delegate without adding real value.
- Reuse and extend what exists before inventing parallel patterns.

## Comments and documentation

House style (fliks / dadi):

- **No narrative `//` or `#` comments** that restate the next line, say “patching this,” or narrate control flow. Delete them or promote intent onto the symbol as formal docs.
- **Formal docs only** on meaningful public exports and non-obvious helpers — not every local.
- **Go:** godoc immediately above the declaration; the first sentence starts with the symbol name. Describe parameters in prose when needed. There is no `@param` / JSDoc tag style in Go.
- **TS/JS:** Prefer documenting on the type or symbol itself. For React components with more than a couple of props, define a `FooProps` interface above the component and put **field docs** there — not `@param props.foo` on the component. Prefer **inline** param docs (fliks style) over `@param` tags when TypeScript already carries types; `@param` / `@returns` are fine on plain functions when they add meaning — do not invent fluff.
- **Python:** docstrings on meaningful exports.
- Manual backfill is encouraged. Do not rely on extreme lint rules to force docs on every local.
- Code should mostly explain itself through naming and structure.

## README shape (multi-module / multi-repo)

- **One `README.md` per repo/module** — no nested feature READMEs, no excess markdown doctrine files in the repo.
- Same section order everywhere:
  1. What this module does in the ecosystem
  2. What it depends on (not what depends on it)
  3. Layout sketch
  4. Config vs env (required; fail at startup)
  5. Local run + CI/CD
  6. Logging / error-code notes when relevant
- Do not create markdown files unless asked (except keeping the module README accurate).

## Tests

- Assert **behavior and effectiveness**: API status + error codes/types, state changes, mappers, request lifecycle.
- Do **not** assert CSS values, copy strings, or snapshot markup as the primary signal.
- Add tests when requested or when they cover real behavior worth protecting (especially for CI).
- Do not add tests that assert the obvious or exist only to increase coverage.

## Dependencies

- Prefer established project dependencies over rolling custom utilities.
- Do not add libraries for problems already solved in the stack.

## Naming

- Names reflect domain language, not implementation detail.
- Files match what they contain. A file named `handlers.go` contains handlers, not unrelated utilities.

## Types

- Shared types live in a dedicated `types/` area (or language equivalent), not scattered through handlers.
- DTOs, domain structs, and API contracts are explicit — not anonymous inline shapes repeated across files.

## Error handling

- Errors propagate. Catch only when you can act on the failure.
- Never swallow errors silently (`catch {}`, `.catch(() => {})`, log-and-continue without reason).
- Surface failures to the caller or user. See system-doctrine for the no-fallback policy.
- Prefer stable error **codes** / `type` fields in API error JSON (`{ error: { type, message } }`) over prose-only failures.

## Configuration

- Required configuration is required. No silent defaults for URLs, credentials, or service endpoints in application code.
- Environment examples (`.env.example`) document every required variable.
- Fail at process startup when required env/config is missing.

## API clients

- Use a proper HTTP client (axios or equivalent) with interceptors, timeouts, and typed responses — not raw fetch scattered across files.
- Direct uploads to third-party storage (presigned URLs) may use whatever mechanism exposes progress events.
- Domain-bucket API modules under `shared/api/<domain>/` (or equivalent); pages do not own tokens or base URLs.

## Observability

- Prefer structured logs (JSON lines) with stable fields: `time`, `level`, `service`, `msg`, plus `code` / `request_id` / request summary when HTTP.
- Log what matters (request outcomes, module failures). Suppress framework boot spam and access-log noise unless debugging.
- Retries are fine when explicit, logged, and bounded — that is resilience, not a fallback.

## CI / CD

- Prefer CI to run the meaningful test/build gate so local machines are not the source of truth.
- Keep workflows consistent across modules: concurrency groups, cancel-in-progress, test before publish.
- Document what CI/CD does in the module README.

## Git

- Never commit unless explicitly asked.
- Never force-push to main/master without explicit request.
