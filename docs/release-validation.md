# Cerberus Release Validation

End-to-end validation of a two-node Cerberus cluster (one Master, one Worker)
before tagging a release. Works with any two machines that can reach each other
over the network.

## 1. Local Release Gate

Run these on the Master machine before the cluster test:

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

## 3. Prepare Wordlist

On both machines, create the same data-dir-relative wordlist:

```bash
export CERBERUS_DATA_DIR="$HOME/cerberus-data"
mkdir -p "$CERBERUS_DATA_DIR/wordlists"
printf "admin\ncerberus123\npassword\n" > "$CERBERUS_DATA_DIR/wordlists/test.txt"
```

Known-good MD5 hashes:

```text
admin        21232f297a57a5a743894a0e4a801fc3
cerberus123  f6be3f2408481885304a362deafa168a
password     5f4dcc3b5aa765d61d8327deb882cf99
```

## 4. Start Master

Find the Master IP address, then start the Master:

```bash
export CERBERUS_DATA_DIR="$HOME/cerberus-data"
bin/cerberus-master serve --listen 0.0.0.0:50051 --public --tls-hosts "<MASTER_IP>"
```

The Master defaults to localhost-only; `--public` is required for a non-loopback
listener.

Copy the generated `server.crt` to the Worker machine. On Linux it is usually:

```text
~/.config/cerberus/server.crt
```

## 5. Issue Worker Token

In a second terminal on the Master machine:

```bash
export CERBERUS_DATA_DIR="$HOME/cerberus-data"
bin/cerberus-master token worker issue --worker-id worker-01
```

Send these two values to the Worker machine:

```text
CERBERUS_WORKER_ID=worker-01
CERBERUS_WORKER_TOKEN=<token>
```

Do not reuse the admin token for a Worker.

## 6. Start the Worker

On the Worker machine:

```bash
export CERBERUS_DATA_DIR="$HOME/cerberus-data"
export CERBERUS_MASTER_ADDR="<MASTER_IP>:50051"
export CERBERUS_TLS_CA="/path/to/server.crt"
export CERBERUS_WORKER_ID="worker-01"
export CERBERUS_WORKER_TOKEN="<token>"

bin/cerberus-worker
```

On the Master, verify:

```bash
bin/cerberus-master worker list
```

Expected: `worker-01` is `healthy`.

## 7. Run the Validation Batch

Create a small batch file on the Master machine:

```bash
cat > /tmp/cerberus-validation-hashes.txt <<'EOF'
21232f297a57a5a743894a0e4a801fc3
f6be3f2408481885304a362deafa168a
5f4dcc3b5aa765d61d8327deb882cf99
EOF
```

Submit it:

```bash
bin/cerberus-master task add-batch --file /tmp/cerberus-validation-hashes.txt --mode md5 --wordlist wordlists/test.txt --chunk 2
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

## 8. Invariants To Confirm

- TLS is enforced on all Master/Worker gRPC traffic.
- Admin and Worker tokens are separate; neither is usable in the other role.
- Worker tokens are bound to a Worker ID and are revocable at any time.
- Wordlist and output paths stay scoped under `CERBERUS_DATA_DIR` unless
  `CERBERUS_ALLOW_UNSAFE_PATHS=1` is set.
- Cracked passwords are hidden from logs and CLI output unless
  `CERBERUS_REVEAL_PASSWORDS=1` is explicitly set.
- The local smoke script repeatedly validates the Master/Worker flow before
  release.

## 9. Release Rule

Commit the validation checkpoint after the local release gate passes. Tag a
release (e.g. `v0.1.0`) only after the two-node cluster test above passes.
