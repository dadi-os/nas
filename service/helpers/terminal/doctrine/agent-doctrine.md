# Agent Doctrine

How AI coding assistants should work on my projects.

## Before writing code

1. Read surrounding files. Match naming, types, import style, and abstractions.
2. Understand whether the task is a question, review, or implementation — do not over-implement answers to questions.
3. Check existing structure before creating new folders or files.
4. Prefer the portable nesting and documentation patterns already proven in fliks.gg / dadi when shaping new code.

## While writing code

- Follow coding-doctrine, structure-doctrine, and system-doctrine.
- Never add fallback systems. If you catch yourself adding `?? 'default'`, `catch {}`, or env defaults — stop and require the real value.
- Pages and route files are construction sites. Do not create container wrappers.
- Minimize compartmentalization. Ask: "does this need to be its own file?" Complex reusable units get a folder with `index` as the public API.
- No narrative mid-body `//` comments. Formal docs only: Go godoc (name-first, no `@param`), TS JSDoc/inline/`FooProps` field docs, Python docstrings — on meaningful exports and non-obvious helpers.
- Run builds/tests after substantive changes. Do not claim success without verifying.
- Across sibling modules, keep structure, docs, CI, and logging consistent so hopping feels transferrable.

## Communication

- Proportional detail — simple fixes do not need essays.
- Use code citations for referencing existing code.
- No engagement bait at the end of responses.
- Do not suggest follow-ups in every message.
- Be honest about completeness when asked; do not claim every file was polished if only the backbone landed.

## Git and PRs

- "Build X" means build it on a feature branch (`feature/<x>`) cut from the up-to-date default branch.
- Commit as you go: one commit per coherent piece of the feature, in the repo's commit-message style.
- Push the branch whenever the repo has a remote. Setting up a remote is not yours to do.
- Nothing is gated on anyone's say-so.
- Use `gh` for GitHub operations when creating PRs.

## What I value

- **Structure** above all — I should know where to look for anything.
- **Directness** — fat handlers, thin shells, no abstraction lasagna.
- **Explicitness** — required config, real errors, no silent degradation.
- **Honest UI** — hardcode simple markup on pages; componentize only complex or reused pieces.
- **Portability** — nested units with a clear public API; shared lib/api bucketed by concern.
- **CI as the gate** — behavior tests in the runner, not “works on my machine.”
- **Logs that matter** — requests, failures, codes — not framework gibberish.

## What I reject

- Fallback chains and silent recovery
- Over-compartmentalization (Wordmark.tsx, Hero.tsx, Faq.tsx for a landing page)
- Premature architecture (services, repositories, entities folders for small apps)
- Drive-by refactors and scope creep
- Creating docs/commits/PRs without being asked
- Narrative implementation comments and excess markdown files
- CSS/string snapshot tests as a substitute for effectiveness tests
