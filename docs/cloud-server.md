# Cloud server

Host memgraph cloud projects for your own machines. For the client, see
[cloud-sync.md](cloud-sync.md).

**Status: unreleased and not ready for real memories.** See "Not done yet".

It is the same `memgraph` program. git does storage and transfer; memgraph adds access
control, size limits, push rules and upkeep. It is unrelated to `memgraph serve`, the
graph explorer.

## Needs

Linux, `git` and coreutils `timeout`. Tested with git 2.34.1 only.

## Set up

    export MEMGRAPH_CLOUD_DATA=/var/lib/memgraph-cloud      # or pass --data
    memgraph cloud admin project create notes
    memgraph cloud admin token create --name laptop --project notes
    memgraph cloud serve                                    # 127.0.0.1:8787

Then on that machine: `memgraph cloud login <https url>`, followed by `cloud enable`
or `cloud clone`.

**Run admin commands as the same user as the server**, or it cannot read what they
create. They work on the data directory, also while the server runs. There is no remote
admin API.

    memgraph cloud admin project list
    memgraph cloud admin token list
    memgraph cloud admin token revoke <id>     # applies from the next request

Use one token per machine, so each can be revoked alone. A token is printed once; only
its hash is stored. There is no command to delete a project yet.

## TLS and network

The server speaks plain HTTP, and requests carry the token. Put a TLS proxy in front.
It refuses a non-loopback address unless you pass `--allow-non-loopback`; do that only
if the network path is already private, such as a container network behind the proxy.
The proxy must allow request bodies of 32 MB and requests lasting 180 s.

## Failed logins

After 20 failed logins in a minute a client gets 429. Requests without credentials do
not count. Tokens are far too long to guess; this limits wasted work and log noise.

Behind a proxy every request comes from the proxy's address. To tell clients apart,
start the server with `--client-ip-header X-Forwarded-For` (or your proxy's header).

- **Only do this if the proxy sets or appends that header and clients cannot reach the
  server directly.** The server uses the last address in it. With more than one proxy
  in front, that is the address of a proxy, not of the client.
- With the header set, a client over the limit is blocked even with a valid token,
  until the minute ends. Machines sharing one public address share that limit.
- Without it, local clients cannot be told apart. Failed logins get 429 after the
  limit, but **a valid token is never refused**.

## Running it as a service

**Stopping the service must also stop its git processes.** At startup the server
removes lock files and temporary data that a killed git leaves behind; a leftover
`main.lock` would block every push. That is only safe if no earlier git process is still
running. A systemd unit does this with its default `KillMode=control-group`. Do not
change that, and do not run git by hand in served repositories while the server is up.

Example unit, **not yet tested**:

    [Service]
    User=memgraph
    Environment=MEMGRAPH_CLOUD_DATA=/var/lib/memgraph-cloud
    ExecStart=/usr/local/bin/memgraph cloud serve
    KillMode=control-group
    Restart=on-failure
    StateDirectory=memgraph-cloud
    UMask=0077

Restart the service after replacing the binary: the push hook runs the binary at the
path the server started from.

## Push rules

A push is accepted only if it:

1. updates `refs/heads/main` and nothing else, and does not delete it
2. starts from the current head (git checks this atomically)
3. has well-formed objects, all present
4. adds only commits with at most one parent, leading to the old head
5. holds, in every new commit, a flat tree of regular files named `ledger.jsonl` or
   `memory_*.md`

git alone does not enforce all of this: rules 1, 4 and 5 rely on the `pre-receive`
hook, which the server installs at startup. If the hook program cannot run, git
rejects the push. But **git accepts every push if the hook file is missing or not
executable**, so:

- At startup the server pushes to a temporary repository, which no token can reach:
  one valid push must be accepted and one forbidden push refused. Otherwise it does
  not start.
- Before each push it checks that the hook file is unchanged and executable. If not,
  it answers 503 to every push and logs `REFUSING PUSHES`. Fetches still work.
  Restarting reinstalls the hook.

The per-push check does not protect against someone who can already write to the
data directory.

The server does not check memory content. A valid token holder can push bad content or
delete memories; client guards and git history limit the damage.

## Limits

| Limit | Value |
|---|---|
| Request body, also after decompression | 32 MB |
| One object, unpacked | 16 MB |
| All objects in a push, unpacked | 128 MB |
| Objects in a push | 100,000 |
| Commits in a push / files per tree | 100 / 20,000 |
| One git process: a request / maintenance | 60 s / 600 s |
| Pushes / fetches handled by git at once | 2 / 4 |
| Request bodies held on disk at once | 8, so up to 256 MB, plus git's temporary files for 2 pushes |
| Wait for a free slot before answering 503 | 10 s, at each of the two steps |
| Receiving headers / a whole request | 10 s / 60 s |
| A request and its response, from the headers on | 180 s |
| Idle connection | 60 s |

Every pack is scanned **before** git sees it. git's own `receive.maxInputSize` limits
only compressed bytes: in testing, without the scan, a push under 0.01 MB made git use
2 GB of memory. Also set a memory limit on the service.

## Upkeep

Every 500 pushes (`--repack-every`) the server compacts the project and removes
leftovers of rejected pushes. The count is kept in the repository, so it survives
restarts; maintenance that was due runs at the next start. After a failure it is
retried 10 pushes later. Pushes wait while it runs; fetches continue. In a test
with 10,000 commits and 3,629 files this took up to about 6 s, and the slowest push
7.5 s.

## Not done yet

- **Power loss:** git 2.34 has no `core.fsync` setting, so an acknowledged push might be
  lost. Untested.
- No backup tooling. Copy the data directory's `repos/` and `tokens.json` to another
  machine. After restoring an old copy, clients stop until they join again.
- The service setup and the proxy have not been tested. Neither has running longer
  than a test.
- One machine with a valid token can occupy all upload slots for up to a request's
  time limit.
- Nothing limits how large a project grows on disk.
- When the server is busy, `cloud sync` fails and has to be run again. With 12 test
  clients syncing at once, 21 runs were needed in one test run.
