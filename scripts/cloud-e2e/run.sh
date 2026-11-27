#!/bin/sh
# Two simulated machines and a server behind a TLS proxy, all in Docker.
# Nothing on the host is mounted and no port is published.
# Usage: scripts/cloud-e2e/run.sh [--keep]      (needs docker and go)
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
WORK=$(mktemp -d)
PASS=0
FAIL=0
NET=mg-test-net
CAP_VM="--cpus 0.5 --memory 384m --memory-swap 384m"

cleanup() {
	[ "${KEEP:-}" = 1 ] && return
	docker rm -f mg-test-proxy mg-test-server mg-test-vm1 mg-test-vm2 mg-test-admin >/dev/null 2>&1
	docker volume rm mg-test-data mg-test-ca >/dev/null 2>&1
	docker network rm $NET >/dev/null 2>&1
	rm -rf "$WORK"
}
trap cleanup EXIT INT TERM
[ "${1:-}" = --keep ] && KEEP=1

ok() { PASS=$((PASS + 1)); echo "  pass  $1"; }
bad() { FAIL=$((FAIL + 1)); echo "  FAIL  $1"; [ -n "${2:-}" ] && echo "$2" | tail -12 | sed 's/^/        /'; }
check() { name=$1; shift; if out=$("$@" 2>&1); then ok "$name"; else bad "$name" "$out"; fi; }
# expect NAME CODE PATTERN command...
expect() {
	name=$1 code=$2 pattern=$3
	shift 3
	out=$("$@" 2>&1)
	got=$?
	if [ $got -eq "$code" ] && echo "$out" | grep -q -- "$pattern"; then ok "$name"; else bad "$name (exit $got, wanted $code and '$pattern')" "$out"; fi
}

mg() { vm=$1; shift; docker exec "mg-test-$vm" memgraph "$@"; }
sh_in() { vm=$1; shift; docker exec "mg-test-$vm" sh -c "$*"; }
memdir() { echo /root/.memgraph/projects/notes/memory; }
find_id() { sh_in "$1" "grep -l -- '$2' $(memdir)/memory_*.md | head -1" | sed 's/.*memory_//; s/\.md$//'; }
has() { sh_in "$1" "grep -rq -- '$2' $(memdir)"; }
lacks() { ! has "$1" "$2"; }
admin() { docker exec mg-test-server memgraph cloud admin "$@"; }
server_log() { docker logs mg-test-server 2>&1; }
server_history() { docker exec mg-test-server git -c safe.directory='*' -C /var/lib/mg/data/repos/notes.git log -p --all; }
server_lacks() { ! server_history | grep -q -- "$1"; }

start_server() {
	docker run -d --init --name mg-test-server --network $NET --network-alias memgraph.test \
		--cpus 1 --memory 768m --memory-swap 768m -v mg-test-data:/var/lib/mg mg-test-server >/dev/null
}
# The proxy shares the server's network, so it must restart when the server does.
start_proxy() {
	docker rm -f mg-test-proxy >/dev/null 2>&1
	docker run -d --name mg-test-proxy --network container:mg-test-server \
		--cpus 0.5 --memory 192m -v mg-test-ca:/data mg-test-proxy >/dev/null
}
wait_https() {
	for _ in $(seq 60); do
		sh_in vm1 'wget -q -O /dev/null https://memgraph.test/health' 2>/dev/null && return 0
		sleep 0.5
	done
	return 1
}

echo "== build"
(cd "$ROOT" && CGO_ENABLED=0 nice -n 19 go build -p 1 -o "$WORK/memgraph" .) || exit 1
cp "$HERE"/Dockerfile.* "$HERE/Caddyfile" "$WORK/"
for img in server vm proxy; do
	nice -n 19 docker build -q -t mg-test-$img -f "$WORK/Dockerfile.$img" "$WORK" >/dev/null || exit 1
done

echo "== start (private network, no outside access)"
docker network create --internal $NET >/dev/null || exit 1
docker volume create mg-test-data >/dev/null
docker volume create mg-test-ca >/dev/null
start_server
start_proxy
for vm in vm1 vm2; do
	docker run -d --init --name mg-test-$vm --hostname $vm --network $NET $CAP_VM \
		-e SSL_CERT_FILE=/root/ca.crt mg-test-vm >/dev/null || exit 1
done
for _ in $(seq 60); do docker cp mg-test-proxy:/data/caddy/pki/authorities/local/root.crt "$WORK/ca.crt" 2>/dev/null && break; sleep 0.5; done
for vm in vm1 vm2; do docker cp "$WORK/ca.crt" mg-test-$vm:/root/ca.crt; done
check "https reaches the server through the proxy" wait_https
expect "server passed its startup self-check" 0 "listening on 127.0.0.1:8787" server_log
mounts=$(docker inspect -f '{{range .Mounts}}{{.Type}} {{end}}' mg-test-vm1 mg-test-vm2 mg-test-server mg-test-proxy)
case "$mounts" in *bind*) bad "no host folder is mounted" "$mounts" ;; *) ok "no host folder is mounted" ;; esac
check "machines have no git" sh_in vm1 '! command -v git'

echo "== set up"
check "create project" admin project create notes
T1=$(admin token create --name vm1 --project notes 2>/dev/null)
T2=$(admin token create --name vm2 --project notes 2>/dev/null)
[ -n "$T1" ] && [ -n "$T2" ] || { bad "tokens created"; exit 1; }
ok "tokens created"
login() { echo "$2" | docker exec -i mg-test-$1 memgraph cloud login https://memgraph.test; }
check "vm1 login" login vm1 "$T1"
check "vm2 login" login vm2 "$T2"
expect "plain http to a remote server is refused" 85 "must use https" mg vm1 cloud login http://memgraph.test
sh_in vm1 "mkdir -p $(memdir)"
check "vm1 registers the project" mg vm1 attach notes --memory-dir "$(memdir)"
for text in "alpha shared fact" "bravo shared fact" "charlie shared fact" "KEEPLOCAL private fact"; do
	mg vm1 --project notes remember "$text" >/dev/null || bad "remember $text"
done
LOCAL_ID=$(find_id vm1 KEEPLOCAL)
check "keep one memory local before enable" mg vm1 cloud local "$LOCAL_ID" --project notes
expect "enable uploads three" 0 "upload (3)" mg vm1 cloud enable --project notes -y
expect "vm2 clones" 0 "download (3)" mg vm2 cloud clone notes -y
expect "vm2 recalls what vm1 saved" 0 "alpha shared fact" mg vm2 --project notes recall "alpha"
check "local-only memory did not travel" lacks vm2 KEEPLOCAL

echo "== two-way"
mg vm2 --project notes remember "delta from vm2" >/dev/null
check "vm2 uploads" mg vm2 cloud sync
check "vm1 downloads" mg vm1 cloud sync
expect "vm1 recalls what vm2 saved" 0 "delta from vm2" mg vm1 --project notes recall "delta"

echo "== offline double edit"
A=$(find_id vm1 "alpha shared")
docker network disconnect $NET mg-test-vm2
mg vm1 --project notes edit "$A" "alpha EDITED ON VM1" >/dev/null
mg vm2 --project notes edit "$A" "alpha EDITED ON VM2" >/dev/null
check "recall works offline" mg vm2 --project notes recall "EDITED"
expect "sync offline fails" 110 "." mg vm2 cloud sync
check "the offline edit is still there" has vm2 "EDITED ON VM2"
docker network connect $NET mg-test-vm2
check "vm1 syncs first" mg vm1 cloud sync
expect "vm2 gets a conflict" 93 "both-changed" mg vm2 cloud sync
check "server version is active on vm2" has vm2 "EDITED ON VM1"
expect "the losing edit is kept" 0 "EDITED ON VM2" mg vm2 cloud conflicts show 1 --project notes
check "restore it" mg vm2 cloud conflicts restore 1 --project notes
check "vm2 shares it" mg vm2 cloud sync
check "vm1 receives it" mg vm1 cloud sync
check "both now agree" has vm1 "EDITED ON VM2"

echo "== deletes"
B=$(find_id vm1 "bravo shared")
mg vm1 --project notes delete "$B" >/dev/null
mg vm1 cloud sync >/dev/null && mg vm2 cloud sync >/dev/null
check "a delete travels" lacks vm2 "memory_$B"
C=$(find_id vm1 "charlie shared")
mg vm1 --project notes delete "$C" >/dev/null
mg vm2 --project notes edit "$C" "charlie still needed" >/dev/null
mg vm1 cloud sync >/dev/null
expect "an edit beats a delete" 93 "edit-restored" mg vm2 cloud sync
mg vm1 cloud sync >/dev/null
check "the edit survives on vm1" has vm1 "charlie still needed"
mg vm2 cloud conflicts resolve 2 --project notes >/dev/null
expect "deleted text is in the other machine's ledger" 0 "bravo shared fact" mg vm2 --project notes ledger --json

echo "== safety"
mg vm1 --project notes delete "$LOCAL_ID" >/dev/null
check "sync after deleting the local-only memory" mg vm1 cloud sync
check "the server never held its text" server_lacks KEEPLOCAL
mg vm1 --project notes remember "paste $T1" >/dev/null
expect "a pasted token blocks the sync" 94 "memgraph-token" mg vm1 cloud sync
mg vm1 --project notes delete "$(find_id vm1 'paste mgc_')" >/dev/null
# Its ledger snapshot still holds the token, so that blocks too.
expect "and still blocks after delete, because the ledger keeps the text" 94 "ledger.jsonl" mg vm1 cloud sync
sh_in vm1 "sed -i '/paste mgc_/d' $(memdir)/ledger.jsonl"
check "clean again" mg vm1 cloud sync
check "no token reached the server" server_lacks "mgc_"

echo "== server restart and crash"
docker restart -t 20 mg-test-server >/dev/null && start_proxy
check "back after a restart" wait_https
mg vm1 --project notes remember "echo after restart" >/dev/null
check "sync after restart" mg vm1 cloud sync
mg vm2 cloud sync >/dev/null
docker kill mg-test-server >/dev/null && docker start mg-test-server >/dev/null && start_proxy
check "back after being killed" wait_https
mg vm2 --project notes remember "foxtrot after kill" >/dev/null
check "sync after the kill" mg vm2 cloud sync
check "vm1 gets it" mg vm1 cloud sync

echo "== backup, lose the server, restore"
backup() { docker exec mg-test-server memgraph cloud admin backup >"$1" 2>"$WORK/backup.log"; }
check "backup to stdout, as a vault would pull it" backup "$WORK/current.tar.gz"
check "an unchanged server gives the same bytes" sh -c "docker exec mg-test-server memgraph cloud admin backup 2>/dev/null | cmp - '$WORK/current.tar.gz'"
lose_and_restore() {
	docker rm -f mg-test-proxy mg-test-server >/dev/null
	docker volume rm mg-test-data >/dev/null && docker volume create mg-test-data >/dev/null || return 1
	docker run --rm -i --network none --memory 384m -v mg-test-data:/var/lib/mg mg-test-server \
		memgraph cloud admin restore <"$1" || return 1
	start_server && start_proxy && wait_https
}
expect "restore into a new server" 0 "Restored 1 project(s) and 2 token(s)" lose_and_restore "$WORK/current.tar.gz"
mg vm1 --project notes remember "golf after restore" >/dev/null
check "vm1 carries on without relinking" mg vm1 cloud sync
check "vm2 carries on without relinking" mg vm2 cloud sync
check "vm2 has the new memory" has vm2 "golf after restore"
expect "restore refuses a live data directory" 110 "not empty" sh -c "docker exec -i mg-test-server memgraph cloud admin restore <'$WORK/current.tar.gz'"

echo "== restore an OLDER backup"
backup "$WORK/older.tar.gz"
mg vm1 --project notes remember "hotel made after the older backup" >/dev/null
D=$(find_id vm1 "delta from vm2")
mg vm1 --project notes delete "$D" >/dev/null
mg vm1 cloud sync >/dev/null && mg vm2 cloud sync >/dev/null
expect "restore the older backup" 0 "Restored 1 project" lose_and_restore "$WORK/older.tar.gz"
expect "vm1 stops instead of deleting" 94 "history" mg vm1 cloud sync
check "vm1 keeps the newer memory" has vm1 "hotel made after"
check "relink: disable" mg vm1 cloud disable --project notes
expect "relink: clone" 0 "upload" mg vm1 cloud clone notes -y
check "vm2 relinks too" sh -c "docker exec mg-test-vm2 memgraph cloud disable --project notes && docker exec mg-test-vm2 memgraph cloud clone notes -y"
mg vm1 cloud sync >/dev/null
check "the newer memory is back on the server for vm2" has vm2 "hotel made after"
if has vm1 "delta from vm2"; then
	echo "  note  a memory deleted after the older backup CAME BACK after relinking"
else
	echo "  note  a memory deleted after the older backup stayed deleted"
fi

echo "== access"
ID2=$(admin token list | awk '$3=="vm2"{print $1}')
check "revoke vm2" admin token revoke "$ID2"
expect "vm2 is refused at once" 110 "unauthorized" mg vm2 cloud sync
guess() { sh_in vm2 'for i in $(seq 30); do wget -q -S -O /dev/null "https://x:wrong$i@memgraph.test/notes.git/info/refs?service=git-upload-pack" 2>&1 | grep -o "HTTP/[0-9.]* [0-9]*"; done'; }
guess_header() { sh_in vm2 'for i in $(seq 30); do wget -q -S -O /dev/null --header "Authorization: Basic eDp3cm9uZw==" https://memgraph.test/notes.git/info/refs?service=git-upload-pack 2>&1 | grep -o "HTTP/[0-9.]* [0-9]*"; done'; }
expect "repeated bad logins get 429" 0 "429" guess_header
check "vm1 is not affected by vm2's failures" mg vm1 cloud sync
expect "the server saw vm2's own address, not the proxy's" 0 "failed logins from \"172\." server_log

echo "== server log"
if server_log | grep -qF -e "$T1" -e "$T2"; then bad "no token in the server log"; else ok "no token in the server log"; fi
if server_log | grep -qE 'panic|REFUSING'; then bad "no panic or hook warning" "$(server_log | grep -E 'panic|REFUSING')"; else ok "no panic or hook warning"; fi
echo "  server log lines mentioning warnings or errors:"
server_log | grep -iE 'warn|deprecat|error|fatal' | sed 's/^[0-9\/: ]*//' | sort | uniq -c | head -8 | sed 's/^/    /'
docker exec mg-test-server git -c safe.directory='*' -C /var/lib/mg/data/repos/notes.git fsck --full --strict >/dev/null 2>&1 && ok "server repository passes fsck" || bad "fsck"

echo
echo "passed $PASS, failed $FAIL"
[ $FAIL -eq 0 ]
