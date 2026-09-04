# Changelog

## 1.10.0 — verify, supersede, and an append-only ledger

- Added `memgraph verify`: extracts the paths, URLs, and repositories a memory
  asserts and reports each as fresh, stale, or unknown. Offline and report-only
  by default; `--net` enables URL/repo checks, `--mark` writes `stale:` and
  `verified:` into frontmatter. Exits `90` when anything is stale.
- Staleness requires positive evidence. A missing file counts only when its
  parent directory exists **under the current user's home**; `/etc`, `/usr` and
  friends describe servers, not this machine. Auth walls (401/403) prove a URL
  is alive; timeouts, unreachable hosts, and memories that mention
  `ssh`/`rcx`/`docker exec`/an IP are `unknown`, never stale.
- Added `memgraph supersede <id> (--with <id> | --text <content>) [--reason]`:
  replaces a memory while keeping the belief it recorded. The old memory keeps
  its content, gains `superseded_by`/`superseded_at`/`superseded_reason`, and is
  hidden from `recall` and `list` unless `--include-superseded` is passed.
- Added `memgraph ledger [--since <when>] [--limit <n>]`: the append-only
  `ledger.jsonl` record of every supersede and delete, each carrying a snapshot
  so a memory stays readable after it is gone. `delete` now writes to it too,
  and refuses to delete if the record cannot be written.
- Added MCP surface: `memgraph_supersede` as a core tool, `verify` and `ledger`
  under `memgraph_admin`. `memgraph_delete` now goes through the ledger.
- Fixed a pre-existing round-trip bug: `formatMemoryFile` re-emitted the blank
  line that `parseMemory` kept, so every rewrite grew the body by a newline at
  each end. Harmless at one edit, unbounded under a `verify --mark` cron.

## 1.9.0 — embedded cli-guide-spec discovery

- Added offline `memgraph guide` JSON with the memory/graph model, canonical loop,
  concepts, command groups, examples, and operational gotchas.
- Added `memgraph guide --human` Markdown rendering and `memgraph help-json` catalog.
- Added `GET /guide` and `GET /llms.txt` to the HTTP server using the same embedded guide.
- Added cold-start and HTTP route tests for guide/catalog conformance.
