# Cerberus Lab Demo Checklist

Use this checklist for the first lab-assistant demo. Keep the demo to one Master laptop and one Worker laptop.

## 1. Local Release Gate

Run these on the Master laptop before the live demo:

```bash
go test ./...
go vet ./...
go test -race ./...
/tmp/cerberus-bin/govulncheck ./...
scripts/smoke-local.sh
CERBERUS_SMOKE_TASKS=5000 CERBERUS_SMOKE_TIMEOUT=120s scripts/smoke-local.sh
scripts/build-release.sh
```

Expected smoke output:

```text
PASS local smoke: tasks=5000 completed=5000 found=5000 failed=0
```

## 2. Build Binaries

```bash
scripts/build-release.sh
```

Artifacts:

```text
bin/cerberus-master
bin/cerberus-worker
```

## 3. Prepare Demo Wordlist

On both laptops, create the same data-dir-relative wordlist:

```bash
export CERBERUS_DATA_DIR="$HOME/cerberus-demo-data"
mkdir -p "$CERBERUS_DATA_DIR/wordlists"
printf "admin\ncerberus123\npassword\n" > "$CERBERUS_DATA_DIR/wordlists/test.txt"
```

Known-good MD5 hashes:

```text
admin        21232f297a57a5a743894a0e4a801fc3
cerberus123  f6be3f2408481885304a362deafa168a
password     5f4dcc3b5aa765d61d8327deb882cf99
```

## 4. Start Master On Lab Network

Find the Master laptop IP address, then start Master:

```bash
export CERBERUS_DATA_DIR="$HOME/cerberus-demo-data"
bin/cerberus-master serve --listen 0.0.0.0:50051 --public --tls-hosts "<MASTER_IP>"
```

Master defaults to localhost-only. The `--public` flag is required for a non-loopback listener.

Copy the generated `server.crt` to the Worker laptop. On Linux it is usually:

```text
~/.config/cerberus/server.crt
```

## 5. Issue Worker Token

In a second terminal on the Master laptop:

```bash
export CERBERUS_DATA_DIR="$HOME/cerberus-demo-data"
bin/cerberus-master token worker issue --worker-id worker-friend-1
```

Send these two values to the Worker laptop:

```text
CERBERUS_WORKER_ID=worker-friend-1
CERBERUS_WORKER_TOKEN=<token>
```

Do not reuse the admin token for a Worker.

## 6. Start Worker Laptop

On the Worker laptop:

```bash
export CERBERUS_DATA_DIR="$HOME/cerberus-demo-data"
export CERBERUS_MASTER_ADDR="<MASTER_IP>:50051"
export CERBERUS_TLS_CA="/path/to/server.crt"
export CERBERUS_WORKER_ID="worker-friend-1"
export CERBERUS_WORKER_TOKEN="<token>"

bin/cerberus-worker
```

On the Master laptop, verify:

```bash
bin/cerberus-master worker list
```

Expected: `worker-friend-1` is `healthy`.

## 7. Run Demo Batch

Create a small batch file on the Master laptop:

```bash
cat > /tmp/cerberus-demo-hashes.txt <<'EOF'
21232f297a57a5a743894a0e4a801fc3
f6be3f2408481885304a362deafa168a
5f4dcc3b5aa765d61d8327deb882cf99
EOF
```

Submit it:

```bash
bin/cerberus-master task add-batch --file /tmp/cerberus-demo-hashes.txt --mode md5 --wordlist wordlists/test.txt --chunk 2
```

Show results:

```bash
bin/cerberus-master task list
bin/cerberus-master task list --table --limit 20
bin/cerberus-master worker list
```

Expected summary:

```text
completed:3 failed:0 found:3
```

## 8. Talking Points

- Cerberus uses TLS for Master/Worker gRPC traffic.
- Admin and Worker tokens are separated.
- Worker tokens are bound to a Worker ID and can be revoked.
- Wordlist and output paths are constrained under `CERBERUS_DATA_DIR` by default.
- Found passwords are hidden unless `CERBERUS_REVEAL_PASSWORDS=1` is explicitly enabled.
- The local smoke script repeatedly validates the Master/Worker flow before demos.

## 9. Release Rule

Commit this checkpoint after the local release gate passes. Create a `v0.1.0` tag only after the two-laptop lab-network test passes.
