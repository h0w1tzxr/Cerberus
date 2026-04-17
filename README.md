<!--
  Cerberus README
  Repo:    https://github.com/h0w1tzxr/Cerberus
  License: GNU GPLv3
-->

<div align="center">

```
   ____           _
  / ___|___ _ __| |__   ___ _ __ _   _ ___
 | |   / _ \ '__| '_ \ / _ \ '__| | | / __|
 | |__|  __/ |  | |_) |  __/ |  | |_| \__ \
  \____\___|_|  |_.__/ \___|_|   \__,_|___/
```

**A Go-based command-and-control system for distributed hash cracking.**
*One Master schedules work. A fleet of Workers cracks chunks in parallel.*

[![License: GPLv3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Status](https://img.shields.io/badge/status-active_development-orange)
![Platform](https://img.shields.io/badge/platform-linux_%7C_macos_%7C_windows-555)

</div>

---

## ⚠️ Acceptable Use

Cerberus is released for research, education, authorized penetration testing,
and recovery of your own hashes. It is a personal engineering project and has
not been audited against operational, legal-hold, or chain-of-custody standards.

Cerberus must not be used by:

- Law enforcement agencies at any level, federal, state, municipal, or international.
- Military, intelligence, or homeland-security services, including contractors acting on their behalf.
- Anyone conducting password recovery against systems they do not own, without explicit written authorization from the owner.

This notice is a statement of intent, not a license term. GPLv3 does not permit
additional use restrictions. If your work falls in the prohibited categories,
please use a tool that is built and supported for your operational requirements
(hashcat with enterprise orchestration, commercial DFIR suites, and so on).

By cloning, installing, or running Cerberus you acknowledge this notice.

---

## What It Is Today

Cerberus is a distributed hash-cracking system written in Go. In its current
form the closest analog is [Hashtopolis](https://github.com/hashtopolis/server):
a coordinator plus authenticated worker agents, an operator UI, a task queue,
per-worker telemetry. Cerberus is a Go-native alternative to that category,
built around a gRPC control plane, bearer-token authentication, TLS on every
link, and a full-screen Bubble Tea terminal UI.

Today's Workers are consensual. They are started by the host operator with a
bearer token that was issued by the Master. They authenticate over TLS, pull
work, and report results. Nothing gets deployed silently. Nothing runs without
the host's knowledge.

Architecturally this is the same command-and-control shape that a botnet uses.
The difference is consent, authentication, and operator control. The shape is
what makes Cerberus scale: throughput grows linearly as Workers join.

Where hashcat maxes out a single host, Cerberus fans one cracking job out
across every Worker you point at the Master. The scheduler tracks per-worker
rate, quarantines misbehaving nodes, auto-evicts silent ones, and re-leases
failed chunks to healthier peers.

---

## Features (Shipping Today)

- Horizontal scale-out with a gRPC Master and Worker pair. Adding a Worker adds throughput.
- Hash modes: MD5 and SHA256. More are on the roadmap.
- Wordlist streaming with a byte-offset index, so multi-gigabyte wordlists do not need to fit in memory.
- Full-screen Bubble Tea operator TUI (Dashboard, Tasks, Workers, Logs, Help) with a `--plain` mode for scripts.
- Fleet health: heartbeat tracking, auto-eviction of silent Workers, quarantine for misbehaving ones, per-chunk retries on healthier peers.
- Task lifecycle: queued, reviewed, approved, running, completed or failed, with pause, resume, priority adjustment, and bulk batch ingest.
- Security baseline: mandatory TLS, separate admin and worker tokens, path sandboxing on wordlists, loopback-only admin RPCs by default.
- Live telemetry: per-worker rate, aggregate cluster throughput, ETA, found and failed counts, per-worker task history.

---

## Architecture

```
                        ┌───────────────────────────┐
                        │          Master           │
                        │                           │
                        │  task queue + state       │
                        │  TUI + admin CLI          │
                        │  TLS listener + tokens    │
                        └──────────────┬────────────┘
                                       │ gRPC (TLS + bearer token)
            ┌──────────────────────────┼──────────────────────────┐
            │                          │                          │
       ┌────▼─────┐               ┌────▼─────┐               ┌────▼─────┐
       │  Worker  │               │  Worker  │               │  Worker  │
       │   pull   │               │   pull   │               │   pull   │
       │   crack  │               │   crack  │               │   crack  │
       │  report  │               │  report  │               │  report  │
       └──────────┘               └──────────┘               └──────────┘
```

The Worker protocol uses five RPCs, all Worker to Master:

| RPC              | Purpose                                 |
|------------------|-----------------------------------------|
| `RegisterWorker` | Announce worker id and CPU cores        |
| `Heartbeat`      | Keep the registration alive             |
| `GetTask`        | Pull the next chunk                     |
| `ReportProgress` | Stream chunk progress                   |
| `ReportResult`   | Submit the chunk outcome (hit or miss)  |

---

## How It Compares

|                         | **Cerberus (today)**                 | **hashcat**                    | **Hashtopolis**                    |
|-------------------------|--------------------------------------|--------------------------------|------------------------------------|
| Topology                | Distributed (Master + Workers)       | Single host                    | Distributed (server + agents)      |
| Language                | Go                                   | C                              | PHP server, Python agent           |
| Transport               | gRPC over TLS + bearer tokens        | (none, local)                  | HTTPS + API keys                   |
| Operator UI             | Built-in full-screen terminal UI     | CLI only                       | Web UI                             |
| Hash algorithms         | MD5, SHA256 (growing)                | 300+                           | Whatever hashcat supports (proxied)|
| GPU acceleration        | CPU only (GPU is on the roadmap)     | CPU and GPU                    | CPU and GPU (via hashcat)          |
| Positioning             | Portfolio project, active dev        | Industry-standard cracker      | Enterprise cracking ops            |

Cerberus is not trying to replace hashcat. The value is the orchestration
layer: turning a set of machines you control into one cooperating cracking
fleet, with a single-terminal operator experience.

---

## Quickstart

```bash
# Terminal 1. Start the Master. The TUI opens if you are attached to a terminal.
go run ./Master

# Terminal 1 (TUI command drawer, press `:`). Issue a Worker token.
token worker issue --worker-id worker-01

# Terminal 2, on the Worker machine. Configure and start the Worker.
export CERBERUS_MASTER_ADDR="<master-ip>:50051"
export CERBERUS_WORKER_ID="worker-01"
export CERBERUS_TLS_CA="/path/to/server.crt"
export CERBERUS_WORKER_TOKEN="<token>"
go run ./Worker

# Terminal 1 (TUI command drawer). Add a task.
task add --hash 21232f297a57a5a743894a0e4a801fc3 --mode md5 --keyspace 100000 --chunk 1000
```

Known-good MD5 hashes for a first smoke test:

```
admin        21232f297a57a5a743894a0e4a801fc3
cerberus123  f6be3f2408481885304a362deafa168a
password     5f4dcc3b5aa765d61d8327deb882cf99
```

---

## Command Reference

Every CLI command is also available inside the TUI by pressing `:`.

| Command                                           | What it does                              |
|---------------------------------------------------|-------------------------------------------|
| `serve [--listen ADDR] [--plain]`                 | Start the Master                          |
| `token worker issue --worker-id ID`               | Issue a one-shot Worker token             |
| `token worker list`                               | Audit Worker token status                 |
| `token worker revoke --worker-id ID`              | Revoke a Worker token                     |
| `task add --hash H --mode md5 ...`                | Add a task                                |
| `task add-batch --file hashes.txt --mode md5 ...` | Bulk-add tasks                            |
| `task list [--status ...] [--table]`              | List tasks                                |
| `task show <task-id>`                             | Show one task in detail                   |
| `task pause \| resume \| retry \| cancel <id>`    | Task lifecycle actions                    |
| `task set-priority --priority N <id>`             | Bump task priority                        |
| `worker list`                                     | List registered Workers                   |
| `worker admit --worker-id ID` (TUI drawer)        | Re-admit a previously evicted Worker      |
| `worker evicted` (TUI drawer)                     | List operator-evicted Worker ids          |
| `dispatch pause \| resume`                        | Gate dispatch globally                    |

Short aliases: `-t` task, `-w` worker, `-d` dispatch. Passing `-h` prints help
at any level (`cerberus task add -h`, and so on).

---

## Operator TUI

The Master TUI opens automatically when you run `cerberus serve` attached to a
terminal. Pass `--plain` to fall back to line-based output for scripts or SSH
sessions without a proper TTY.

Tabs: **Dashboard · Tasks · Workers · Logs · Help**

| Key          | Action                                   |
|--------------|------------------------------------------|
| `←` / `→`    | Switch tabs                              |
| `↑` / `↓`    | Move the cursor                          |
| `enter`      | Open detail view (Workers tab)           |
| `:`          | Command drawer (run any CLI command)     |
| `/`          | Filter (Tasks and Logs tabs)             |
| `o`          | Cycle task sort (name, fastest, and so on) |
| `p` / `r`    | Pause and resume dispatch                |
| `?`          | Help tab                                 |
| `q`          | Quit                                     |

---

## Configuration

On first run the Master creates a config directory and writes:

- `server.crt`, `server.key`: self-signed TLS certificate and key.
- `admin.token`: admin CLI token.
- `worker_tokens.json`: hash of every issued Worker token.
- `workers/<id>.token`: per-Worker tokens.

Default locations:

- Linux: `~/.config/cerberus`
- macOS: `~/Library/Application Support/cerberus`
- Windows: `%APPDATA%\cerberus`

Environment variables:

| Variable                        | Purpose                                            |
|---------------------------------|----------------------------------------------------|
| `CERBERUS_MASTER_ADDR`          | Worker target address                              |
| `CERBERUS_WORKER_ID`            | Worker identity                                    |
| `CERBERUS_WORKER_TOKEN`         | Worker bearer token                                |
| `CERBERUS_ADMIN_TOKEN`          | Admin CLI token                                    |
| `CERBERUS_TLS_CA`               | Path to the Master cert (client trust anchor)     |
| `CERBERUS_TLS_CERT` / `_KEY`    | Override Master TLS material                       |
| `CERBERUS_TLS_SERVER_NAME`      | Override client SNI                                |
| `CERBERUS_TLS_HOSTS`            | Comma-separated SANs for a generated cert          |
| `CERBERUS_LISTEN_ADDR`          | Master listen address (default `127.0.0.1:50051`)  |
| `CERBERUS_PUBLIC=1`             | Allow non-loopback binds                           |
| `CERBERUS_ADMIN_REMOTE=1`       | Allow admin RPCs from non-loopback clients         |
| `CERBERUS_DATA_DIR`             | Where wordlists and output files live              |
| `CERBERUS_ALLOW_UNSAFE_PATHS=1` | Permit file paths outside `CERBERUS_DATA_DIR`      |
| `CERBERUS_REVEAL_PASSWORDS=1`   | Show cracked passwords in logs and CLI output      |

By default the Master binds to `127.0.0.1:50051` and admin RPCs are
localhost-only. To serve a cluster across multiple machines:

```bash
go run ./Master serve --listen 0.0.0.0:50051 --public --tls-hosts "<master-ip>,<master-host>"
```

---

## Project Layout

```
Master/           gRPC server, admin CLI, TUI
Worker/           gRPC client
Common/security/  TLS and token helpers
Common/wordlist/  Streaming and byte-offset index
Common/console/   Inline renderer and ANSI tags
cracker/          Protobuf and generated stubs
```

---

## Security Model

Cerberus is designed to run inside a trusted operator network: a private
subnet, a VPN, a dedicated VLAN, or a reachable compute pool you control. The
transport and authorization properties are:

- **TLS on every link.** The Master refuses plaintext gRPC. A self-signed cert is generated on first run. Operators can supply their own via `CERBERUS_TLS_CERT` and `CERBERUS_TLS_KEY`, and pin `CERBERUS_TLS_SERVER_NAME` on clients.
- **Separated credentials.** Worker tokens and the admin token are distinct. Workers authenticate to pull chunks. The admin token gates task and fleet control. They are not interchangeable.
- **Least-privilege admin.** Admin RPCs are loopback-only unless `--admin-remote` is explicitly passed.
- **Path sandboxing.** Wordlist paths are resolved under `CERBERUS_DATA_DIR` unless `CERBERUS_ALLOW_UNSAFE_PATHS=1` is set, which prevents arbitrary disk reads.
- **Sensitive-value redaction.** Cracked passwords are hidden from logs and CLI output unless `CERBERUS_REVEAL_PASSWORDS=1` is explicitly set.

Operational guidance: rotate tokens periodically, keep `server.crt` and
`server.key` owner-only, and do not publish the gRPC port to the public
internet without another layer (WireGuard, Tailscale, an SSH tunnel).

### Evicting a Worker

`worker delete` in the TUI is authoritative. It removes the Worker from the
Master's active set and also writes the id to `evicted_workers.txt` in the
config directory. Any future registration attempt from that id is refused
with `PermissionDenied`, so a running Worker process cannot silently rejoin.
Run `worker admit --worker-id <id>` from the TUI drawer to clear the record
and let the Worker register again. `worker evicted` lists everyone currently
on the evicted list. Auto-eviction after missed heartbeats does not add the
Worker to this list; the Worker rejoins as soon as it reappears.

---

## What Is Coming Next

The next few milestones, in priority order:

1. **Stabilize the current generation.** Polish the consensual-worker build so it is a genuinely reliable Hashtopolis-class tool: bug fixes, more test coverage, bcrypt and NTLM hash modes, release binaries.
2. **Zero-touch worker onboarding.** A one-shot installer that bootstraps a Worker with just a Master address.
3. **mTLS and CA issuance.** Replace manual bearer-token provisioning with short-lived client certificates signed by a Master CA.
4. **Bloodhound mode.** A planned offensive-operations feature. It is the long-term differentiator. See the next section.

---

## Planned: Bloodhound Mode

Bloodhound is the future of Cerberus and is not shipped yet. Everything in
this section is forward-looking design intent. Do not expect any of it to work
today.

The design intent is to make Cerberus interesting for authorized offensive
engagements. When Bloodhound ships, deploying a Worker to a target host will
be enough for that host to join the cracking fleet, without prior token
issuance and without a host-operator confirmation step. Bloodhound is built
for red-team operators who already have written authorization to run code on
the target. The Acceptable Use notice at the top of this README still applies
in full. Law enforcement and military use is still prohibited.

Design goals for Bloodhound (in priority order):

1. **Non-consensual enrollment.** A Worker that reaches the Master successfully is admitted, with or without the host operator's prior approval. This is the actual command-and-control model.
2. **Backtracking resistance.** A compromised or recovered Worker should not cheaply expose the Master address, the operator identity, or other Workers. This implies hardened transport, hardened on-disk artifacts, and careful minimization of anything the Worker needs to remember.
3. **Operator safety rails.** The TUI displays an unambiguous indicator when Bloodhound is active, because the risk profile while it is on is meaningfully different.

Bloodhound will not ship until the current consensual-Worker generation is
stable and polished. Until then, current Cerberus is what exists.

---

## License

Cerberus is distributed under the [GNU General Public License v3.0](LICENSE).
See the Acceptable Use notice at the top of this README for usage intent.
