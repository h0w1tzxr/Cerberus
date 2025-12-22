package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	pb "cracker"

	"google.golang.org/grpc"
)

const (
	Port          = ":50051"
	ChunkSize     = 1000
	TotalKeyspace = 100000
	TaskTimeout   = 10 * time.Second
)

// ===== MASTER SERVER =====
type MasterServer struct {
	pb.UnimplementedCrackerServiceServer

	tasks     []*pb.Task
	taskIndex int
	mu        sync.Mutex

	found bool
}

// ===== HASH FUNCTION =====
func hashMD5(input string) string {
	h := md5.Sum([]byte(input))
	return hex.EncodeToString(h[:])
}

// ===== gRPC: GET TASK =====
func (s *MasterServer) GetTask(ctx context.Context, _ *pb.Empty) (*pb.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Jika password sudah ditemukan, hentikan distribusi
	if s.found {
		return &pb.Task{}, nil
	}

	if s.taskIndex >= len(s.tasks) {
		return &pb.Task{}, nil
	}

	task := s.tasks[s.taskIndex]
	s.taskIndex++

	log.Printf("[MASTER] Mengirim Task ID %d\n", task.Id)
	return task, nil
}

// ===== gRPC: REPORT RESULT =====
func (s *MasterServer) ReportResult(ctx context.Context, r *pb.Result) (*pb.Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.Found && !s.found {
		s.found = true
		log.Println("====================================")
		log.Println(" PASSWORD DITEMUKAN :", r.Password)
		log.Println(" TASK ID           :", r.TaskId)
		log.Println("====================================")
	}

	return &pb.Ack{Ok: true}, nil
}

// ===== MAIN =====
func main() {
	log.Println("[MASTER] Starting Cerberus Master (gRPC)")

	// ===== TARGET (DEMO) =====
	targetPassword := "golang"
	targetHash := hashMD5(targetPassword)

	log.Println("[MASTER] Target Hash:", targetHash)

	// ===== GENERATE TASKS =====
	var tasks []*pb.Task
	taskID := int32(1)

	for i := 0; i < TotalKeyspace; i += ChunkSize {
		end := i + ChunkSize
		if end > TotalKeyspace {
			end = TotalKeyspace
		}

		words := []string{}
		for j := i; j < end; j++ {
			words = append(words, fmt.Sprintf("%05d", j))
		}

		task := &pb.Task{
			Id:    taskID,
			Words: words,
		}

		tasks = append(tasks, task)
		taskID++
	}

	log.Printf("[MASTER] Total Task: %d\n", len(tasks))

	// ===== INIT SERVER =====
	server := &MasterServer{
		tasks: tasks,
	}

	lis, err := net.Listen("tcp", Port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterCrackerServiceServer(grpcServer, server)

	log.Println("[MASTER] gRPC running on", Port)
	grpcServer.Serve(lis)
}
