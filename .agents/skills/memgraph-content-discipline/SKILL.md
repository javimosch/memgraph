---
name: memgraph-content-discipline
description: What belongs in memgraph memories and what doesn't. Apply before every memgraph save — memories should capture decisions, gotchas, and context the code cannot tell you, not duplicate how the code works. Triggers before any `memgraph save` or `memgraph remember` call.
---

# memgraph-content-discipline

Code is the source of truth for **how**. Memories are the source of truth for
**why, what was tried and abandoned, and what will bite you**. Confuse these and
you create a stale, inferior copy of the code that rots on every commit.

## The core principle

**Memorize what the code CANNOT tell you.** The code shows the winner, not the
alternatives. It shows what, not why. It shows correct behavior, not failure
modes. No single repo shows cross-codebase relationships.

| Code already tells you | Code CANNOT tell you |
|---|---|
| File structure, function signatures, step-by-step flow | Why a decision was made, what tradeoffs were weighed |
| What the current code does | What was tried and abandoned (deleted branches, rejected approaches) |
| Correct behavior | Failure modes that cost real time to discover |
| One repo's internals | How two codebases interact end-to-end |
| Current config values | Why a config value is what it is, what breaks if you change it |
| The code that shipped | The organizational/historical context behind it |

## The 4-question gate

Before every `memgraph save`, run the content through these four questions. If
any answer is "no" for the bulk of what you're about to save, stop and rewrite.

1. **Could an agent discover this by reading the code?** If yes in 1-2 files,
   don't save it. If it requires tracing 5+ files across 2+ repos, a concise
   summary of the *insight* (not the mechanism) is valuable.
2. **Will this be true in 6 months?** If it depends on current file paths,
   function names, or code structure, it will go stale. If it's a decision,
   gotcha, or historical fact, it's durable.
3. **Did this cost real time to discover?** If it was obvious from reading the
   code, don't memorialize it. If it took debugging, a failed experiment, or
   cross-repo investigation, save it.
4. **Is this about WHY, not WHAT?** Save the why. The what is in the code.

## What belongs in memories

- **Decisions and rationale**: "chose SSO redirect over iframe embed because
  widgets are now used directly in MA" — the code shows only the winner.
- **Abandoned approaches**: "iframe POC is on branch DATA-63068-poc, don't
  resurrect" — the code shows no trace of what was rejected.
- **Gotchas that cost real time**: "jest roots=[__tests__,src] so plugin tests
  at plugins/*/__tests__/ are orphaned — put runnable tests at repo-root
  __tests__/ with simpliciti* names" — you'd have to debug silent test
  failures to discover this.
- **Cross-codebase relationships**: "data-v3 calls MA POST /api/sso, MA
  ssoResolve provisions user+assistants, JWT validated via apiv3
  check_token" — no single repo shows this end-to-end.
- **Non-obvious failure modes**: "the :3011 POC server wedges after ~2 days —
  responses arrive N-1 bytes then hang until client timeout" — you'd have to
  experience this to know it.
- **Invisible context**: "this was originally built in adminv3 by mistake, then
  ported" — the code shows no trace of this history.

## What does NOT belong in memories

- **File listings** — `ls` exists and is always current.
- **Function signatures** — the code is the source of truth.
- **Step-by-step code flow** — read the code. If the flow spans repos, save the
  *insight* ("the SSO resolve hook provisions X then Y then Z, and the gotcha
  is..."), not the step-by-step.
- **Architecture descriptions that mirror code structure** — read the code.
- **Config values visible in config files** — read the config. Save *why* a
  value is what it is, not the value itself.
- **Anything an agent could discover by reading 1-2 files** — that's cheap.

## The fuzzy middle: mechanism vs insight

Gotchas often require explaining some mechanism to be useful. The line is:

- **Bad**: "ssoResolve.js has 8 steps. Step 1 validates JWT. Step 2 provisions
  user. Step 3 ensures role. Step 4..." — this is a code walkthrough that goes
  stale when steps are renumbered.
- **Good**: "ssoResolve provisions user+role+assistants+widget-auth in one
  pass. The gotcha: it pre-reads both gates so steady-state re-login is
  zero-write. The footgun: deprovisionWidgetsBuilder uses
  DBService.deleteOne({organizationId}) NOT AssistantManagerService.deleteAssistant
  which matches {name} alone and would delete another org's widget builder."

The mechanism is mentioned only to anchor the gotcha. The gotcha is the point.

## Self-critique before saving

Before hitting enter on a `memgraph save`, re-read your text and strike out
every sentence that describes what the code does. What's left should be
decisions, gotchas, context, and cross-refs. If nothing's left, you weren't
about to save a memory — you were about to duplicate the code.

## Concrete examples (from real memories)

**Good** (decision + abandoned approach + cross-ref):
> data-v3 #63068: chose SSO redirect over iframe embed because widgets are now
> used directly in MA. Iframe POC (2 commits, ~1288 lines) preserved on
> DATA-63068-poc — do not resurrect. MA-side embed infra (publishable keys,
> embed:authorize hook) remains for other embedders.

**Bad** (duplicates `ls` + code reading):
> The simpliciti-workspace plugin has these files in lib/: ssoResolve.js,
> authorizeEmbedShared.js, authorizeAssistantShared.js, inferReturnUrl.js,
> grantGeoredSharedAssistants.js, deprovisionWidgetsBuilder.js...

**Good** (the non-obvious insight from that same plugin):
> simpliciti-workspace seeders are deliberately NOT in the assistants[] array
> because AutoSeedService would create assistants wired to tools that don't
> exist yet — seed scripts provision their own OpenAPI server+tool+DataStash
> via their own main().

## Relationship to other skills

- **memgraph-document**: the *process* for creating a project-index memory +
  thin skill pair. This skill is the *content discipline* that applies to every
  `memgraph save`, including (but not limited to) the memories
  memgraph-document guides you to write.
- **calibrate-skills**: the same principle applies — calibrate captures
  *caveats and gotchas*, not re-explanations of how the tool works.
