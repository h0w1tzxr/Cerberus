package main

import (
        "context"
        "fmt"
        "log"
        "net"
        "sync"
        "time"

        pb "cracker/cracker"
        "google.golang.org/grpc"
)

const (
        Port           = ":50051"
        ChunkSize      = 1000  // Setiap task berisi 1000 percobaan password
        TotalKeyspace  = 100000 // Demo: Keyspace 00000-99999 (Angka saja untuk demo cepat)
        TaskTimeout    = 10 * time.Second // Waktu max worker mengerjakan tugas
)

// Server struct                                                               type server struct {
        pb.UnimplementedCrackerServiceServer
        mu           sync.Mutex
        nextIndex    int64
        targetHash   string
        found        bool
        password     string
@@ -128,26 +126,25 @@ func (s *server) ReportResult(ctx context.Context, in *pb.CrackResult) (*pb.Ack,
func main() {
        // Setup Demo: Hash dari "88888" (MD5)
        // Anda bisa ganti ini dengan hash lain
        target := "21218cca77804d2ba1922c33e0151105"

        lis, err := net.Listen("tcp", Port)
        if err != nil {
                log.Fatalf("failed to listen: %v", err)
        }

        s := grpc.NewServer()
        srv := &server{
                targetHash:  target,
                activeTasks: make(map[string]taskLease),
        }

        pb.RegisterCrackerServiceServer(s, srv)
        log.Printf("Master Hash Cracker running on port %s", Port)
        log.Printf("Target Hash: %s", target)
        log.Printf("Ready for Workers...")

        if err := s.Serve(lis); err != nil {
                log.Fatalf("failed to serve: %v", err)
        }
}
