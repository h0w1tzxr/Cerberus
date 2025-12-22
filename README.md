# Cerberus Hash Cracker (gRPC Distributed Demo)

Proyek ini adalah demo sederhana **distributed hash cracking** berbasis gRPC dengan arsitektur **Master–Worker**. Master membagi keyspace ke beberapa chunk, worker melakukan brute force dan melaporkan hasilnya.

## Prasyarat

- Go 1.22+
- Akses jaringan antara mesin Master dan Worker (port `50051` harus terbuka)

## Struktur Singkat

- `Master/Master.go` — server gRPC (Master) yang membagikan pekerjaan dan menerima hasil.
- `Worker/Worker.go` — klien gRPC (Worker) yang meminta task dan melakukan brute force.
- `cracker/` — file protobuf dan hasil generate gRPC.

## Walkthrough Lengkap (Cara Pakai)

### 1) Clone & Masuk ke Folder

```bash
git clone <repo-url>
cd Cerberus
```

### 2) Pastikan Dependensi Go Terpasang

```bash
go mod tidy
```

### 3) Jalankan Master (Server gRPC)

Di **mesin Master**:

```bash
go run ./Master
```

Output yang diharapkan (contoh):

```
Master Hash Cracker running on port :50051
Ready for Workers...
```

> **Catatan:** Master sekarang memakai alur manual (review → approve → dispatch). Task ditambahkan lewat CLI admin.

### 4) Konfigurasi Worker (IP Master)

Di **mesin Worker**, ubah alamat Master di `Worker/Worker.go`:

```go
// Ganti IP ini dengan IP Laptop Master saat Demo
const MasterAddress = "<IP_MASTER>:50051"
```

Contoh:

```go
const MasterAddress = "192.168.1.10:50051"
```

### 5) Jalankan Worker

```bash
go run ./Worker
```

Output yang diharapkan (contoh):

```
Worker Starting: worker-...
Processing chunk: 0 - 1000
Processing chunk: 1000 - 2000
...
I FOUND THE PASSWORD! Exiting...
```

### 6) Tambahkan & Kelola Task (Manual Flow)

Tambah task dengan mode hash yang eksplisit (md5 atau sha256):

```bash
go run ./Master task add --hash <hash> --mode md5 --keyspace 100000 --chunk 1000
```

Jika memakai wordlist:

```bash
go run ./Master task add --hash <hash> --mode sha256 --wordlist wordlist.txt --chunk 1000
```

> **Catatan:** Path wordlist harus tersedia di mesin Worker (path yang sama).

Review, approve, lalu dispatch agar worker mulai mengambil chunk:

```bash
go run ./Master task review <task-id>
go run ./Master task approve <task-id>
go run ./Master task dispatch <task-id>
```

Lihat status dan progress:

```bash
go run ./Master task list
go run ./Master task show <task-id>
```

### 7) Tambahkan Banyak Worker (Distributed)

Jalankan beberapa Worker di mesin lain (atau terminal lain) untuk mempercepat proses:

```bash
go run ./Worker
```

Master akan membagi task ke setiap worker yang terhubung.

## Cara Kerja

1. Worker mengirim request `GetTask` ke Master.
2. Master memberi `TaskChunk` berisi range indeks dan target hash.
3. Worker melakukan brute force di range tersebut.
4. Worker melaporkan hasil via `ReportResult`.
5. Jika password ditemukan, Master menandai selesai dan worker berhenti.

## Tips Demo

- Untuk demo cepat, gunakan `--keyspace 100000` dan `--chunk 1000`.
- Jika ingin lebih cepat lagi, kecilkan `--keyspace` atau `--chunk`.
- Pastikan port `50051` tidak diblok oleh firewall.
