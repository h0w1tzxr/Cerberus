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
	Port          = ":50051"
	ChunkSize     = 1000   // Setiap task berisi 1000 percobaan password
	TotalKeyspace = 100000 // Demo: Keyspace 00000-99999 (Angka saja untuk demo cepat)
	TaskTimeout   = 10 * time.Second
)

type taskLease struct {
	start      int64
	end        int64
	workerID   string
	assignedAt time.Time
}

type server struct {
	pb.UnimplementedCrackerServiceServer
	mu          sync.Mutex
	nextIndex   int64
	targetHash  string
	found       bool
	password    string
	activeTasks map[string]taskLease
}

func (s *server) GetTask(ctx context.Context, in *pb.WorkerInfo) (*pb.TaskChunk, error) {
	workerID := in.GetWorkerId()

	for {
		s.mu.Lock()

		if s.found {
			s.mu.Unlock()
			return &pb.TaskChunk{NoMoreWork: true}, nil
		}

		now := time.Now()
		for taskID, lease := range s.activeTasks {
			if now.Sub(lease.assignedAt) > TaskTimeout {
				delete(s.activeTasks, taskID)
				task := s.assignTaskLocked(lease.start, lease.end, workerID, now)
				s.mu.Unlock()
				log.Printf("Reassigned task %s to %s: %d-%d", taskID, workerID, lease.start, lease.end)
				return task, nil
			}
		}

		if s.nextIndex < TotalKeyspace {
			start := s.nextIndex
			end := start + ChunkSize
			if end > TotalKeyspace {
				end = TotalKeyspace
			}
			s.nextIndex = end

			task := s.assignTaskLocked(start, end, workerID, now)
			s.mu.Unlock()
			log.Printf("Assigned task %s to %s: %d-%d", task.TaskId, workerID, start, end)
			return task, nil
		}

		if len(s.activeTasks) == 0 {
			s.mu.Unlock()
			return &pb.TaskChunk{NoMoreWork: true}, nil
		}

		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *server) ReportResult(ctx context.Context, in *pb.CrackResult) (*pb.Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.activeTasks[in.TaskId]; ok {
		delete(s.activeTasks, in.TaskId)
	}

	if in.Success && !s.found {
		s.found = true
		s.password = in.FoundPassword
		log.Printf("Password found by %s: %s", in.WorkerId, in.FoundPassword)
	}

	return &pb.Ack{Received: true}, nil
}

func (s *server) assignTaskLocked(start, end int64, workerID string, now time.Time) *pb.TaskChunk {
	taskID := fmt.Sprintf("task-%s-%d", workerID, now.UnixNano())
	s.activeTasks[taskID] = taskLease{
		start:      start,
		end:        end,
		workerID:   workerID,
		assignedAt: now,
	}

	return &pb.TaskChunk{
		TaskId:     taskID,
		StartIndex: start,
		EndIndex:   end,
		TargetHash: s.targetHash,
	}
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
