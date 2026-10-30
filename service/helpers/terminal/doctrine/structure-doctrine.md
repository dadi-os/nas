# Structure Doctrine

## Core principle

Structure is not bureaucracy — it is how you navigate a codebase without thinking. Files should look like what they are. Hopping modules should feel transferrable.

## Pages and routes are construction sites

- Route files (pages, handlers entrypoints) assemble the screen or endpoint.
- They own orchestration: wiring layout, state, and child modules together.
- Do not hide page construction behind container components that exist only to import and render one child (`FeedContainer` wrapping `FeedPage` logic is wrong).
- Thin route shells that delegate everything are wrong. The page builds the page.

## When to make a component or module

Create a separate component/module only when it is:

1. **Complex** — non-trivial UI, animation, SVG engineering diagram, multi-step interaction
2. **Reused** — genuinely used in more than one place
3. **Self-contained domain unit** — e.g. an interaction panel that owns its own state machine

Do **not** create components for:

- Static text blocks that belong on the page
- One-line wrappers
- Tiny presentational fragments (wordmarks, labels, section headers)
- Every CSS grouping

Hardcode simple markup on the page. Inline simple data arrays (FAQ items, feature lists, stats) on the page file.

## Portable nested units (fliks style)

Complex reusable units get a **folder** with:

- `index.tsx` / `index.ts` as the **public API** (construction site for that unit)
- Nested subdomains colocated until proven reusable elsewhere
- Optional `desktop.tsx` / `mobile.tsx` only when the **React tree or interaction model diverges** — not for padding tweaks

Example:

```
shared/components/FlikDisplay/
  index.tsx              # public props + picks desktop/mobile
  desktop.tsx | mobile.tsx
  interactionPanel/
  details/
```

## Folder layout (conceptual)

```
types/          shared domain types and API contracts
features/       domain-specific code grouped by feature
  feed/
  upload/
  auth/
shared/         cross-cutting reusable surface
  api/          client + domain buckets (identity/, flik/, …)
  lib/          bucketed by concern (platform/, content/, media/, ux/)
  components/   portable nested UI units
  hooks/
  layout/       platform shell only (mode, metrics, chrome) — not feature trees
providers/      app-wide context (platform, transitions, auth shell)
pages/          route construction sites
```

Backend mirror:

```
cmd/            binary entrypoints — wiring only
internal/
  http/         route table (construction site)
  types/        shared structs
  game/         shared game validation/tokens
  upload/       upload handlers + limits
  user/         user handlers
  auth/         auth middleware
  config/       env loading
  db/ storage/ queue/   infrastructure clients
```

## Platform layout vs composition

- **Platform** (`shared/layout`): mode detection, viewport metrics, shell chrome, gates, transitions.
- **Composition**: how *this* unit arranges children — stays colocated with the unit (`desktop.tsx` / `mobile.tsx` when trees diverge).
- Never read `innerWidth` for mode decisions — use the shared layout mode hook/tokens.

## Anti-patterns

- `components/` folders full of one-off section components for a single page
- `entities/` god folders mixing types, mock data, and business logic
- `hooks/` top-level folders for hooks used in one place (colocate instead)
- Deep nesting (`features/feed/components/interactionPanel/components/`)
- Thin `constants/` packages for values that belong to one feature (`upload/limits`)
- Splitting handlers into controller → service → repository for five endpoints
- Flat `shared/` util dumps — bucket by concern instead

## Collapsing

- Prefer single files over folders when the folder would hold one file (`shared/game.ts` not `shared/game/index.ts + tokens.ts + meta.ts`).
- Merge related handlers in one file until length makes navigation hard (~400+ lines).
- Split oversized construction sites into nested portable folders when navigation suffers.

## Colocation

- Keep code near where it is used until reuse is proven.
- Feature-specific limits, mocks, and rank data live inside the feature, not globally.

## Growing the codebase

When adding a new domain (feed, ratings, search):

1. Add types to `types/`
2. Add handlers to `internal/<domain>/handlers.go` (or `features/<domain>/`)
3. Wire one line in the route construction site
4. Add API function in `shared/api/<domain>/` using the shared client

Do not pre-build infrastructure for endpoints that do not exist yet.

## Imports

- Path aliases for clarity (`@/types`, `@/shared/api`).
- No try/catch around imports. No dynamic import as a fallback for missing modules.
- Lazy loading is a load strategy, not a fallback — use it deliberately for code splitting, not to mask missing files. Prefer not to lazy-load as recovery.

## Consistency across modules

- When a workspace has sibling modules (services + client + infra), the same layout and documentation principles should transfer module to module.
- Prefer convention + mirrored patterns over premature shared private packages.

## README and docs

- One structured README per module/repo (see coding-doctrine).
- Keep `.env.example` in sync with required config.
- Do not create extra markdown files unless asked.
