# Cloud sync

Share one memgraph project between your own machines through a git server you run.

**Status: unreleased.** The server is `memgraph cloud serve`; see
[cloud-server.md](cloud-server.md), including what it does not do yet. Another git
smart-HTTP server with a `main` branch also works, but without memgraph's push rules it
does not protect the repository from a faulty or hostile client.

- **Opt-in per project.** Nothing syncs until you enable it, and only when you run
  `sync`. `recall` and `save` never contact the server.
- **No installed git is needed on the client.**
- **The server operator can read everything you sync.** Nothing is encrypted by memgraph.

## Use

    memgraph cloud login https://memory.example.com      # token on stdin
    memgraph cloud enable --project notes                # first machine, empty server project
    memgraph cloud clone notes                           # other machines
    memgraph cloud sync
    memgraph cloud status

`enable` and `clone` show what the first sync will do and ask before doing it. URLs must
use HTTPS; plain HTTP is accepted for localhost only. The token is stored in
`~/.memgraph/sync/credentials.json`, readable by you only.

## What syncs

`memory_*.md` files and `ledger.jsonl`. Indexes, `graph.json` and everything else stay
local. Files named `memory_*.md` with characters outside `A-Z a-z 0-9 . _ -` are
reported and not synced.

## Merge rules

Each machine remembers what it last agreed on with the server (the base). "-" means
absent.

| Row | Base | Local | Server | Result |
|---|---|---|---|---|
| 1 | - | x | - | upload |
| 2 | - | - | x | download |
| 3 | - | x | x | nothing |
| 4 | - | x | y | conflict |
| 5 | a | a | a | nothing |
| 6 | a | x | a | upload edit |
| 7 | a | a | y | download edit |
| 8 | a | x | x | nothing |
| 9 | a | x | y | conflict |
| 10 | a | - | a | delete on server |
| 11 | a | a | - | delete here |
| 12 | a | - | - | nothing |
| 13 | a | x | - | the edit wins and is restored on the server |
| 14 | a | - | y | the edit wins and is restored here |

Whole files are merged, not lines or fields. The ledger is the union of both sides'
lines.

**Conflicts:** the server version stays active. Yours is saved outside the memory
directory, so `recall` never returns both. `sync` exits 93 while conflicts are open.

    memgraph cloud conflicts --project notes
    memgraph cloud conflicts show 1 --project notes
    memgraph cloud conflicts restore 1 --project notes   # use your version
    memgraph cloud conflicts resolve 1 --project notes   # keep the active one

On a machine's first sync, an old copy of a memory that the server's ledger shows as
deleted (same ID and content) is set aside as a conflict, not uploaded. Deletions that
left no ledger entry cannot be detected.

## Keeping a memory local

    memgraph cloud local <id> --project notes

This writes `local: true` into the memory; it works before `enable` too. The memory and
the ledger lines that carry its content are never uploaded, and a replacement made with
`supersede --text` stays local. `cloud unlocal` reverses it.

- Flagging an **already synced** memory removes nothing from the server or its history.
  The file is then neither uploaded nor updated.
- Older memgraph versions drop the flag when they rewrite a file. A linked machine keeps
  its own list of local-only IDs to cover this. **`cloud disable` retires that list**, so
  check the flags before joining again.
- `supersede --with <existing-id>` does not change the replacement's flag.

## Guards

A blocked sync exits 94 and changes no memory, locally or on the server.

- **Secrets:** new or changed lines are matched against common key and token formats.
  This misses many secrets. Remove the text, keep the memory local, or accept a finding
  with `cloud allow <fingerprint>` (`--allow-finding` with `enable` and `clone`).
  Git history keeps anything already pushed.
- **Mass deletion:** more than 10 memories, or 10% if that is larger, needs
  `--allow-mass-delete`. A memory directory that lost all its memories always stops.
- **Rewritten server history**, for example a restored backup, stops the sync. Run
  `cloud disable`, then `cloud clone`. That is a first link: memories missing from the
  restored server are uploaded again.
- **Server content:** the client refuses any path or file type other than those above.
- **Ingest:** `graph-from-dir` and the other ingest paths replace every memory file in
  their target, so they refuse a linked directory.

## Concurrent writes

memgraph writes memory files atomically and takes `.memgraph.lock` in the memory
directory, which sync takes too. A file that changes while a sync is running is not
overwritten; the next sync merges it. **Restart long-running processes such as
`memgraph mcp` after upgrading:** an older one does not take the lock. Editors and
scripts do not take it either.

## Local sync data

Each linked project keeps a copy of the last synced snapshot in
`~/.memgraph/sync/<project>/repo.git`, so its size follows the size of the memories,
not the number of syncs. It is a cache: if it is damaged, the next sync downloads the
project again with its history. A first clone also downloads the whole history, which
grows over time.

## Exit codes

0 synced, 93 synced with open conflicts, 94 blocked by a guard, 110 error.
