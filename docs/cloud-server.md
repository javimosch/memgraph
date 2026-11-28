# Cloud server

Host memgraph cloud projects for your own machines. For the client, see
[cloud-sync.md](cloud-sync.md).

**Status: new in 1.12.0 and not yet ready for real memories.** See "Not done yet".

It is the same `memgraph` program. git does storage and transfer; memgraph adds access
control, size limits, push rules and upkeep. It is unrelated to `memgraph serve`, the
graph explorer.

## Needs

Linux and `git`. Tested with git 2.34.1, 2.39.5, 2.43.0 and 2.53.0. The server sets
git's flush settings by version. The published binary needs glibc, so it does not run
on Alpine as it is.

The `timeout` command is not needed: the server bounds each git process, and the
processes git starts, with its own `memgraph cloud limit`. (The `timeout` of Ubuntu
26.04 leaves those child processes running.)

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

With Traefik (tested with 3.7.0, file provider, default timeouts), a route like this
was enough. Use your own host name and certificate resolver. The DNS record pointed
straight at Traefik; with another proxy in front, such as a CDN, check the client
address and the request limits again.

    http:
      routers:
        memgraph-cloud:
          rule: "Host(`memgraph.example.com`)"
          service: memgraph-cloud
          entryPoints: [websecure]
          tls:
            certResolver: letsencrypt
      services:
        memgraph-cloud:
          loadBalancer:
            servers:
              - url: "http://127.0.0.1:8787"

Test large uploads through your proxy: a very slow link was not tried.

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

This unit ran on Ubuntu 24.04 (systemd 255). The limits suit a small shared host;
adjust them to yours.

    [Unit]
    Description=memgraph cloud sync server
    After=network-online.target
    Wants=network-online.target

    [Service]
    User=memgraph
    Group=memgraph
    Environment=MEMGRAPH_CLOUD_DATA=/var/lib/memgraph-cloud
    ExecStart=/usr/local/bin/memgraph cloud serve --listen 127.0.0.1:8787 --client-ip-header X-Forwarded-For
    # The server relies on this: a restart must leave no git running.
    KillMode=control-group
    Restart=on-failure
    RestartSec=5
    StateDirectory=memgraph-cloud
    StateDirectoryMode=0700
    UMask=0077

    # memgraph gives way before other services.
    MemoryMax=384M
    MemorySwapMax=0
    CPUQuota=50%
    Nice=10
    OOMScoreAdjust=500
    TasksMax=128

    NoNewPrivileges=true
    ProtectSystem=strict
    ProtectHome=true
    PrivateTmp=true
    PrivateDevices=true
    ProtectKernelTunables=true
    ProtectKernelModules=true
    ProtectControlGroups=true
    RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
    RestrictSUIDSGID=true
    LockPersonality=true

    [Install]
    WantedBy=multi-user.target

Create the account first:

    useradd --system --home-dir /var/lib/memgraph-cloud --no-create-home \
      --shell /usr/sbin/nologin memgraph

`--client-ip-header` assumes the proxy below; read "Failed logins" before using it.
With this unit, run admin commands like this (the shell does not have the service's
environment):

    sudo -u memgraph memgraph cloud admin token list --data /var/lib/memgraph-cloud

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
| One git process, with its children: a request / maintenance | 60 s / 600 s |
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

## Backup and restore

    memgraph cloud admin backup --data <dir> > backup.tar.gz     # or --out <file>
    memgraph cloud admin restore --data <new dir> < backup.tar.gz   # or --in <file>

A backup holds a git bundle of every project, the token hashes and a manifest. memgraph
does not schedule backups, keep old ones or copy them elsewhere: use your backup tool.
The command writes to stdout, so a tool that collects a command's output can pull it,
for example over SSH. Run it as the server's user. For a tool with a restore command,
pipe the archive into `restore` with a **new** data directory.

- It is safe while the server runs, including during pushes and upkeep. Each project
  is taken at one moment, but not all projects at the same moment.
- It stages one bundle at a time in the data directory, so it needs free space about
  the size of the largest project.
- Unchanged data gives identical bytes (same git version), so tools that skip
  unchanged backups can. Upkeep may change the bytes without changing the data.
- The manifest is written last. A cut-off backup does not restore. Still treat a
  non-zero exit as a failed backup.
- It is **not encrypted**, and holds every synced memory.

**Restore** only creates a new or empty directory, and creates nothing unless every
project passes `git fsck` and matches the manifest. Stop the server, restore to a new
directory, then point the server at it.

- **A backup brings back tokens revoked after it was made.** Check `token list` and
  revoke them again, or use `--without-tokens` and issue new ones.
- If the backup is current, machines carry on as before.
- If it is older than what a machine last synced, that machine's `sync` stops and
  deletes nothing. On each, run `cloud disable`, then `cloud clone`. That is a first
  link: newer memories are uploaded again, **and memories deleted after the backup can
  come back**. `disable` also retires the machine's local-only list, so check `local:
  true` flags before joining again.

## Measured

All with generated memories. These show what one test needed, not guarantees.

- **Containers, 8,832 memories (37 MB)**, server limited to 384 MB without swap and
  half a CPU, git 2.43.0, Caddy in front: first uploads, clones, repeated and
  simultaneous syncs, upkeep and two restores had no failures. The server stayed
  under 90 MB, including file cache. Its data was about 37 MB and a backup about
  32 MB. With all memories in one project, a client peaked at about 250 MB.
- **A one-CPU host with 1.7 GB of RAM**, the unit and a Traefik route as above,
  3,688 memories: sync between two clients, a service restart and the failed-login
  limit worked. In the first run the server's memory peaked at 14 MB; after a
  further clone and upload, at about 35 MB. Edit syncs took 2 to 4 s. A backup taken
  there restored in a container, with all memories and a clean `git fsck`.

The history in these tests was short. How a project grows over months was not
measured.

## Testing with containers

`scripts/cloud-e2e/run.sh` runs two machines without git, the server and a TLS proxy
in Docker, on a network with no outside access. It mounts nothing from the host.

## Not done yet

- **Power loss is untested.** With git 2.36 or later the server sets `core.fsync=all`,
  which covers refs. Older git only offers `core.fsyncObjectFiles`, so there an
  acknowledged push is more likely to be lost. Use 2.36 or later if you can.
- Backups are not encrypted, and nothing schedules them.
- The unit and the Traefik route have only had short trials; nothing here has run for
  long with real use. A reboot of the host was not tested. Caddy was tested in
  containers only.
- Nothing monitors the service. `GET /health` answers 200 while it runs, without a
  token; it does not say whether pushes are being refused (see "Push rules").
- A project cannot be deleted with a command.
- One machine with a valid token can occupy all upload slots for up to a request's
  time limit.
- Nothing limits how large a project grows on disk.
- When the server is busy, `cloud sync` fails and has to be run again. With 12 test
  clients syncing at once, 21 runs were needed in one test run.
