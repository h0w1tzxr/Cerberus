a# Cerberus Hash Cracker (gRPC Distributed Demo)

Cerberus adalah demo distributed hash cracking berbasis gRPC dengan arsitektur Master-Worker. Fokus utamanya adalah kontrol manual oleh operator untuk task lifecycle, tetapi strukturnya tetap siap untuk automation di masa depan.

## Ringkas

- Manual workflow: review -> approve -> dispatch -> running -> completed/failed/canceled.
- Admin CLI untuk kontrol task dan monitor worker.
- Hash mode eksplisit: md5 dan sha256.
- Wordlist streaming + indexing untuk file besar.
- Health dan inflight tracking per worker.

## Prasyarat

- Go 1.22+
- Port `50051` harus terbuka antara Master dan Worker.
- Jika memakai wordlist, path file harus valid di Master (saat add task) dan Worker (saat eksekusi).

## Struktur Project

- `Master/` - gRPC server + admin CLI.
- `Worker/` - gRPC client (worker).
- `Common/wordlist/` - wordlist streaming + indexing.
- `cracker/` - protobuf + generated stubs.

## Quickstart

### 1) Clone dan install dependency

```bash
git clone <repo-url>
cd Cerberus
go mod tidy
```

### 2) Jalankan Master

```bash
go run ./Master
```

Output contoh:

```
Master Hash Cracker running on port :50051
Ready for Workers...
```

### 3) Konfigurasi Worker

Di mesin Worker, ubah alamat Master di `Worker/Worker.go`:

```go
const MasterAddress = "<IP_MASTER>:50051"
```

### 4) Jalankan Worker

```bash
go run ./Worker
```

### 5) Tambah task + jalankan workflow manual

```bash
go run ./Master task add --hash <hash> --mode md5 --keyspace 100000 --chunk 1000
go run ./Master task review task-1
go run ./Master task approve task-1
go run ./Master task dispatch task-1
```

## Arsitektur dan Data Flow

### Komponen utama

- **CrackerService** (gRPC untuk worker): `GetTask`, `ReportResult`.
- **CrackerAdmin** (gRPC untuk operator): add/list/show task, apply action, list worker, pause/resume dispatch.
- **Master State**:
  - Task registry + queue priority.
  - Worker registry (last seen, inflight).
  - Wordlist cache dan index.
  - Dispatch gate (pause global).

### Alur data (ringkas)

1. Operator membuat task via Admin CLI.
2. Master memvalidasi input (mode, wordlist, keyspace).
3. Operator review + approve task.
4. Operator dispatch task (task jadi dispatch-ready).
5. Worker `GetTask` -> Master assign chunk.
6. Worker brute force -> `ReportResult`.
7. Master update progress dan status.

## Task Lifecycle (detail)

Status yang dipakai:

- `queued`: task baru dibuat.
- `reviewed`: task sudah direview operator.
- `approved`: task siap diproses (masih perlu `dispatch`).
- `running`: task sedang berjalan (ada chunk aktif).
- `completed`: task selesai (password ditemukan atau keyspace habis).
- `failed`: task gagal (contoh: wordlist error).
- `canceled`: task dibatalkan oleh operator.

Action dan efek:

- **review**: `queued -> reviewed`.
- **approve**: `reviewed -> approved`.
- **dispatch**: set `dispatch_ready=true` agar worker bisa mengambil chunk.
- **pause**: stop assign chunk baru untuk task ini.
- **resume**: task boleh diproses lagi (jika dispatch-ready).
- **cancel**: stop task, clear leases aktif, hasil late tidak dipakai.
- **retry**: reset progress task gagal kembali ke `approved` (perlu dispatch ulang).
- **set-priority**: naik/turunkan prioritas di queue.

## Admin CLI - Referensi Lengkap

Semua perintah dijalankan lewat `go run ./Master ...`. Binary yang sama akan bertindak sebagai CLI bila ada argumen.

### Global flags

- `--addr` (default `localhost:50051`) - alamat gRPC Master.
- `--operator` (default `USER` atau `operator`) - identitas operator.

### Task add (single)

```bash
go run ./Master --addr <master:50051> --operator alice task add \
  --hash <hash> \
  --mode md5 \
  --keyspace 100000 \
  --chunk 1000 \
  --priority 5 \
  --max-retries 3
```

Keterangan flags:

- `--hash` target hash.
- `--mode` md5 atau sha256 (wajib).
- `--keyspace` total kombinasi untuk range numeric.
- `--chunk` ukuran chunk.
- `--priority` angka lebih besar diproses lebih dulu.
- `--max-retries` batas retry task gagal.
- `--wordlist` (opsional) path wordlist.

Jika pakai wordlist:

```bash
go run ./Master task add --hash <hash> --mode sha256 --wordlist wordlist.txt --chunk 1000
```

Catatan:

- Jika `--wordlist` dipakai, `--keyspace` akan diabaikan.
- Master akan membangun index wordlist dan menghitung total baris.

### Task add-batch (file atau stdin)

```bash
go run ./Master task add-batch --file hashes.txt --mode md5 --keyspace 100000 --chunk 1000
```

```bash
cat hashes.txt | go run ./Master task add-batch --stdin --mode sha256 --wordlist wordlist.txt --chunk 500
```

Tambahan:

- `--file -` juga membaca dari stdin.
- Baris kosong diabaikan.

### Task review/approve/dispatch

```bash
go run ./Master task review task-1 task-2
go run ./Master task approve task-1 task-2
go run ./Master task dispatch task-1 task-2
```

### Task pause/resume (per task)

```bash
go run ./Master task pause task-1
go run ./Master task resume task-1
```

Efek:

- `pause` menghentikan assign chunk baru untuk task tersebut.
- Chunk yang sudah berjalan tetap lanjut sampai selesai.

### Task cancel + reason

```bash
go run ./Master task cancel --reason "wrong hash" task-1
```

Efek:

- Task langsung `canceled`.
- Chunk aktif di-clear (hasil late diabaikan).

### Task retry (manual)

```bash
go run ./Master task retry task-1
```

Efek:

- Task `failed` direset jadi `approved`.
- Progress dan pending range direset.
- Perlu `dispatch` ulang agar worker mengambil chunk.

### Task priority

```bash
go run ./Master task set-priority --priority 10 task-1
```

### Task list + filter

```bash
go run ./Master task list
go run ./Master task list --status queued,reviewed,approved,running,failed
```

### Task show (detail)

```bash
go run ./Master task show task-1
```

### Dispatch pause/resume (global)

```bash
go run ./Master dispatch pause
go run ./Master dispatch resume
```

Efek:

- Saat paused, Worker akan menerima `dispatch paused` dan menunggu.

### Worker list (health + inflight)

```bash
go run ./Master worker list
```

Contoh output:

```
WORKER    CORES  LAST_SEEN              INFLIGHT  HEALTH
worker-A  4      2025-01-01T10:00:00Z   2         healthy
```

## Wordlist Behavior (Detail)

- Index dibangun saat `task add` / `add-batch`.
- Index menyimpan offset setiap N baris (default stride 1024).
- Worker membaca wordlist secara streaming per range, bukan load penuh.
- Batas panjang line default 1 MiB per line.
- Buffer reader default 128 KiB.
- Jika wordlist tidak ada di Worker, task akan gagal dengan error message dan perlu `retry`.

## Range Task Behavior (Detail)

- `--keyspace` menentukan range 0..keyspace-1.
- Worker melakukan zero-padding sesuai jumlah digit `keyspace-1`.
  Contoh: `keyspace=100000` -> candidate `00000` sampai `99999`.
- `--chunk` menentukan ukuran range per assignment.

## Reliability dan Failure Modes

- Lease timeout 10s: chunk yang tidak dilaporkan akan di-requeue.
- Worker health `stale` jika last seen > 30s.
- Task bisa `failed` jika:
  - Wordlist tidak ditemukan di Worker.
  - Line wordlist terlalu panjang.
  - Error I/O saat membaca.
- Task `failed` harus `retry` lalu `dispatch` ulang.

## Troubleshooting

**Worker selalu "dispatch paused"**
- Pastikan task sudah `review`, `approve`, dan `dispatch`.
- Pastikan global `dispatch` tidak paused.

**Task tidak bergerak**
- Cek `task show` dan `task list` apakah status masih `queued` atau `reviewed`.
- Cek `dispatch_ready` dan `paused`.

**Task gagal karena wordlist**
- Pastikan path wordlist sama di Master dan Worker.
- Pastikan file tidak kosong dan line tidak terlalu panjang.

**Worker tidak muncul di list**
- Pastikan Worker sudah berjalan dan bisa konek ke Master.
- Cek port `50051` tidak diblok firewall.

## Benchmark (Wordlist)

```bash
go test -bench . ./Common/wordlist
```

## Development Notes

Regenerate protobuf stubs:

```bash
PATH="$GOPATH/bin:$PATH" protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  cracker/cracker.proto
```

## Demo Tips

- Mulai dengan `--keyspace 100000` dan `--chunk 1000`.
- Jalankan beberapa Worker untuk percepatan.
- Gunakan `worker list` untuk cek health.
- Gunakan `task list` untuk monitoring progress.
