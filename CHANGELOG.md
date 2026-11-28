# Changelog

## 1.12.4 — MCP refuses what it does not understand

- **Fixed:** MCP tools ignored arguments they did not know, so
  `memgraph_delete` with `dry_run: true` deleted the memory. A value of the wrong
  type was ignored too: a `project` given as a number meant the current
  repository.
- **Changed:** every MCP tool and `memgraph_admin` command now refuses an unknown
  argument, a value of the wrong type, and `arguments` or `args` that are not an
  object. Nothing is done, and the error says what is accepted. `null` counts as
  absent.
- **Changed:** `memgraph_admin attach` refuses a mode other than `register`,
  `remove` or `rebind` (a typo used to register), and `from_scope` without
  `rebind`.

## 1.12.3 — text is not metadata, and writes need an exact target

- **Fixed:** a `---` line inside a memory was read as the edge of the file's
  header. `recall` and `read` hid the text up to the next one, text there could
  set the memory's type, project or tags, `supersede` removed it from the file
  and `delete` left it out of the ledger copy. Files hit by this need no repair
  unless one of those two commands rewrote or removed them.
- Fixed: `edit` and `supersede` dropped header fields memgraph does not know.
- **Changed:** memory IDs must be given in full, on the command line and over
  MCP. `delete 1` used to pick a memory whose ID ended in `_1`.
- **Changed:** a command that writes now fails for a `--project` name that
  resolves to nothing, or that is unregistered and matches several folders. It
  used to write to the current repository, or to the first match. The current
  repository's own name still works.
- **Changed:** commands that change something refuse flags they do not take, and
  flags missing their value. `delete <id> --dry-run` used to delete. Put `--`
  before text that starts with a dash.
- **Changed:** MCP can no longer purge. `detach --purge` deletes only a folder
  that is one project's own: not the folder of all projects, not one another name
  uses, and not one outside `~/.memgraph/projects`.
- **Changed:** `attach` needs `--force` to move a name from a folder that exists.
- Fixed: a `projects.json` that could not be read was silently overwritten, and
  simultaneous changes to it lost entries. It is now locked, replaced in one
  step, and left alone when damaged.
- Fixed: a skill graph build could still replace a memory made by `import` or
  `demo` that claimed to be generated.

## 1.12.2 — serve no longer deletes memories

- **Fixed, data loss:** `memgraph serve`, run in a project, deleted that
  project's memories when it synced skills, which it does by default when it
  finds skill directories. No ledger entry was written. `serve --memory-dir` and
  `graph-from-dir --memory-dir` did the same to the named folder. A graph build
  now removes and replaces only files that a graph build wrote, never a saved
  memory, and `serve` writes synced skills to the global skill graph. **Do not
  run `serve` with 1.12.1 or earlier.** If you did, look for the memories in a
  backup or in version control.
- Fixed: `init` (command line and MCP) replaced an existing `MEMORY.md`.
- Fixed: `setup` replaced an edited `.devin/skills/memgraph-usage/SKILL.md`.
- Fixed: a section heading quoted in a code block of `AGENTS.md` could be taken
  for memgraph's section.

## 1.12.1 — stabilisation: local-only web view, and files left alone

- **Changed:** `serve` now listens only on `127.0.0.1`. It has no login, and used
  to listen on every network interface. Pass `--host 0.0.0.0` (or an address) if
  another machine or a container needs to reach it.
- Fixed: `bridge`, and `setup` in a repository with a `.claude`, `.opencode` or
  `.copilot` folder, replaced `.claude/CLAUDE.md`, `.opencode/memory.json` or
  `.copilot/settings.json` without asking. An existing file with other content is
  now left unchanged (`bridge` exits 92).
- Fixed: the MCP `setup` command replaced the whole `AGENTS.md`. It now adds or
  updates one section, as the command line does.
- Fixed: updating that section deleted everything after it up to the next `##`
  heading, including text under a `#` heading. An `AGENTS.md` that cannot be read
  is no longer replaced.
- Fixed: shortening text could cut a character such as `é` or `日` in half. `edit`
  and `supersede` could write that into a memory file, and since 1.12.0 previews
  could show codes such as `\xc3`. Files already affected are not changed.
- Fixed: `plans` (command line and MCP) failed after `graph-from-dir
  --include-plans` had indexed plans into the global graph.
- Documentation matches the program again. Cloud sync is marked experimental and
  paused.

## 1.12.0 — opt-in cloud sync, and search that needs far less memory

- Search reads the memory files directly instead of keeping
  `search_index.json`, and uses far less memory: measured on 3,688 memories, a
  search limited to 10 results went from about 290 MB to 16 MB in similar time.
  Saving, editing and syncing no longer rebuild anything. Each search reads
  every file, so very large projects on slow disks may notice. Matches and
  scores are as before; equal scores now list the newest ID first.
- Fixed: search could silently miss memories added or changed by `rsync`, git,
  a hand edit, with `auto_index` off, or after an interrupted rebuild.
- `auto_index` no longer does anything. Old `search_index.json` files are
  removed when a memory directory is next read.
- New `memgraph cloud` commands: opt-in, manual sync of a project between
  machines through a git smart-HTTP server, with conflict copies, local-only
  memories, secret scanning and deletion guards. See `docs/cloud-sync.md`.
- New `memgraph cloud serve` and `memgraph cloud admin` to host projects: git
  does storage; memgraph adds per-machine tokens, size limits, push rules and
  upkeep, a startup check that the push rules work, and limits on failed logins
  and uploads. Not ready for real memories yet; see `docs/cloud-server.md`.
- New `memgraph cloud admin backup` and `restore` for the server's data.
- `cloud local <id>` also works on a deleted memory: its ledger history stays on
  the machine while the deletion syncs.
- The server uses `core.fsync` with git 2.36 or later.
- Memory files are now written atomically, and writers lock the memory
  directory (`.memgraph.lock`). Restart long-running `memgraph mcp` processes
  after upgrading.
- New `local: true` memory field. `delete` and `supersede` record it in ledger
  snapshots.
- Building memgraph now needs Go 1.26 or later, and it has its first
  dependency, go-git.

## 1.11.1 — quieter warnings and a global-flag-friendly command line

- Registry integrity warnings now only fire where a mismatch can actually
  misroute a write: a recorded `remote` whose scope dir exists on disk
  (repo-local writes land there while `--project` resolves the registered
  path), or an alias shadowing a same-named scope dir. `remote` is
  metadata — `path` alone drives resolution — so entries created before
  the attach fix or by auto-import, whose remote names no live scope dir,
  are reclassified as `drift`: listed in `memgraph projects` (and its
  `--json` output) but silent everywhere else. Other commands emit at
  most one summary line — `N registry entries need attention; run
  'memgraph projects'` — instead of the full report on every invocation.
- Added `memgraph projects --repair` (and `repair:true` on the MCP
  `memgraph_projects` tool): rewrites drifted `remote` metadata to the
  memory dir's real scope — the same value `attach` records today — in
  one command. Entries whose remote scope dir exists are skipped and
  reported, since both scopes are live and picking one is a data
  decision.
- `attach` still warns at registration time when the new alias collides
  with an existing scope dir.
- Fixed command detection when a value-taking global flag precedes the
  command: `memgraph --project aplomb config` read `aplomb` as the command
  and exited 85. The scanner now drives off one table of value-taking
  flags, and argv is normalized so the command always sits at index 1 —
  flags before the command now reach handlers that parse `os.Args[2:]`.
- Fixed `memgraph --version`/`--help`/`-v`/`-h` exiting 85: a leading
  help/version flag now counts as the command instead of being skipped by
  the scanner.

## 1.11.0 — project aliases you can rename, remove, and repair

- Graph explorer UI: the galaxy view is now titled "Memgraph — Galaxy" and
  carries a `galaxy` badge, with Inter as the interface font. `graph.js`
  drops its IIFE wrapper — the file is already an ES module, so the wrapper
  only cost a level of indentation — and folds `init`/`initThree` into
  module top-level. Behaviour is unchanged. The UI is embedded in the
  binary via `go:embed`, so it ships inside this release's asset.
- Fixed `memgraph attach <name> --memory-dir <path>` recording the current
  directory's git remote as the project `remote` instead of the scope the
  memory dir actually belongs to. The scope key is now always derived from
  the memory dir's parent directory name — for
  `~/.memgraph/projects/<scope>/memory` that is `<scope>` — on both the CLI
  and MCP surfaces. The wrong remote made `--project <name>` silently write
  into an unrelated scope.
- Registry integrity warnings: on every run, entries whose `remote`
  contradicts their memory dir's scope — and aliases that shadow a real
  scope dir of the same name while pointing elsewhere — are reported on
  stderr. `memgraph projects` also lists them under `warnings`, and
  `attach` warns at registration time when the new alias collides with an
  existing scope dir.
- Fixed `attach` parsing `os.Args[2:]` directly: a global flag before the
  command (e.g. `memgraph --json attach x`) registered the literal name
  "attach". It now parses the command tail like `detach`/`rename`.
- Added `memgraph detach <name> [--purge]` (alias `unregister`): removes a
  project alias from `~/.memgraph/projects.json` without touching the memory
  files. `--purge` also deletes the registered memory dir — it asks for
  confirmation interactively (skipped under `--json`/`-y`) and refuses any
  path outside `~/.memgraph/projects`, so a corrupted registry entry cannot
  turn it into an arbitrary directory delete.
- Added `memgraph rename <old> <new>`: renames a registered alias in place,
  keeping the memory dir, remote key, and original registration time. Fails
  if the old name is missing or the new one is taken.
- Added `detach` and `rename` to `memgraph_admin` (MCP), the `help-json`
  catalog, and the embedded guide's command groups.

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
