# Cerberus Hash Cracker (gRPC Distributed Demo)

Proyek ini adalah demo sederhana **distributed hash cracking** berbasis gRPC dengan arsitektur **Master–Worker**. Master membagi keyspace ke beberapa chunk, worker melakukan brute force dan melaporkan hasilnya.

## Prasyarat

- Go 1.22+
- Akses jaringan antara mesin Master dan Worker (port `50051` harus terbuka)

## Struktur Singkat

- `Master.go` — server gRPC (Master) yang membagikan pekerjaan dan menerima hasil.
- `Worker.go` — klien gRPC (Worker) yang meminta task dan melakukan brute force.
- `cracker/` — file protobuf dan hasil generate gRPC.

## Walkthrough Lengkap (Cara Pakai)

### 1) Clone & Masuk ke Folder

```bash
git clone https://github.com/h0w1tzxr/Cerberus.git
cd Cerberus
```

### 2) Pastikan Dependensi Go Terpasang

```bash
go mod tidy
```

### 3) Jalankan Master (Server gRPC)

Di **mesin Master**:

```bash
go run Master/Master.go
```

Output yang diharapkan (contoh):

```
Master Hash Cracker running on port :50051
Target Hash: 21218cca77804d2ba1922c33e0151105
Ready for Workers...
```

> **Catatan:** Target hash default adalah MD5 dari string `"88888"`.

### 4) Konfigurasi Worker (IP Master)

Di **mesin Worker**, ubah alamat Master di `Worker.go`:

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
go run Worker/Worker.go
```

Output yang diharapkan (contoh):

```
Worker Starting: worker-...
Processing chunk: 0 - 1000
Processing chunk: 1000 - 2000
...
I FOUND THE PASSWORD! Exiting...
```

### 6) Tambahkan Banyak Worker (Distributed)

Jalankan beberapa Worker di mesin lain (atau terminal lain) untuk mempercepat proses:

```bash
go run Worker.go
```

Master akan membagi task ke setiap worker yang terhubung.

## Cara Kerja

1. Worker mengirim request `GetTask` ke Master.
2. Master memberi `TaskChunk` berisi range indeks dan target hash.
3. Worker melakukan brute force di range tersebut.
4. Worker melaporkan hasil via `ReportResult`.
5. Jika password ditemukan, Master menandai selesai dan worker berhenti.

## Tips Demo

- Untuk demo cepat, biarkan `TotalKeyspace` di `Master.go` (default 100000).
- Jika ingin lebih cepat lagi, kecilkan `TotalKeyspace` atau `ChunkSize`.
- Pastikan port `50051` tidak diblok oleh firewall.
