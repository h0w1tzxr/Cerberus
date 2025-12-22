package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	pb "hashcracker/cracker" // Asumsikan nama module folder anda 'hashcracker'
	"google.golang.org/grpc"
)

const (
	Port           = ":50051"
	ChunkSize      = 1000  // Setiap task berisi 1000 percobaan password
	TotalKeyspace  = 100000 // Demo: Keyspace 00000-99999 (Angka saja untuk demo cepat)
	TaskTimeout    = 10 * time.Second // Waktu max worker mengerjakan tugas
)

// Server struct
type server struct {
	pb.UnimplementedCrackerServiceServer
	mu           sync.Mutex
	nextIndex    int64
	targetHash   string
	found        bool
	password     string
	activeTasks  map[string]taskLease // Melacak task yang sedang dikerjakan (Fault Tolerance)
}

type taskLease struct {
	workerID  string
	startTime time.Time
	startIdx  int64
	endIdx    int64
}

func (s *server) GetTask(ctx context.Context, in *pb.WorkerInfo) (*pb.TaskChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Cek jika password sudah ketemu
	if s.found {
		return &pb.TaskChunk{NoMoreWork: true}, nil
	}

	// 2. Fault Tolerance: Cek task yang expired (Worker mati/disconnect)
	// Jika ada task yang timeout, berikan task itu lagi ke worker ini
	now := time.Now()
	for tID, lease := range s.activeTasks {
		if now.Sub(lease.startTime) > TaskTimeout {
			log.Printf("[SCHEDULER] Task %s expired (Worker %s dead?). Re-assigning to %s", tID, lease.workerID, in.WorkerId)
			
			// Update lease
			s.activeTasks[tID] = taskLease{
				workerID:  in.WorkerId,
				startTime: now,
				startIdx:  lease.startIdx,
				endIdx:    lease.endIdx,
			}

			return &pb.TaskChunk{
				TaskId:     tID,
				StartIndex: lease.startIdx,
				EndIndex:   lease.endIdx,
				TargetHash: s.targetHash,
			}, nil
		}
	}

	// 3. Jika keyspace habis
	if s.nextIndex >= TotalKeyspace {
		return &pb.TaskChunk{NoMoreWork: true}, nil
	}

	// 4. Buat chunk baru (Adaptive Work Stealing: Worker yang cepat akan sering kesini)
	start := s.nextIndex
	end := start + ChunkSize
	if end > TotalKeyspace {
		end = TotalKeyspace
	}
	s.nextIndex = end

	taskID := fmt.Sprintf("task-%d", start)
	
	// Simpan lease
	s.activeTasks[taskID] = taskLease{
		workerID:  in.WorkerId,
		startTime: now,
		startIdx:  start,
		endIdx:    end,
	}

	log.Printf("[ASSIGN] Task %s (%d-%d) -> Worker %s", taskID, start, end, in.WorkerId)

	return &pb.TaskChunk{
		TaskId:     taskID,
		StartIndex: start,
		EndIndex:   end,
		TargetHash: s.targetHash,
	}, nil
}

func (s *server) ReportResult(ctx context.Context, in *pb.CrackResult) (*pb.Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.activeTasks, in.TaskId) // Hapus dari tracking active task

	if in.Success {
		if !s.found {
			s.found = true
			s.password = in.FoundPassword
			log.Printf("\n!!! SUCCESS !!!\nPassword FOUND by %s: %s\nTask: %s\n", in.WorkerId, in.FoundPassword, in.TaskId)
		}
	} else {
		// Log progress ringan
		// log.Printf("[DONE] Task %s by %s (Not found)", in.TaskId, in.WorkerId)
	}

	return &pb.Ack{Received: true}, nil
}

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

