package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	pb "cracker/cracker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ganti IP ini dengan IP Laptop Master saat Demo
const MasterAddress = "localhost:50051"

func main() {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = fmt.Sprintf("%d", time.Now().Unix())
	}
	workerID := "worker-" + hostname // Atau gunakan UUID random

	log.Printf("Worker Starting: %s", workerID)

	// 1. Connect ke Master via gRPC
	conn, err := grpc.Dial(MasterAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewCrackerServiceClient(conn)

	// 2. Loop Work Stealing
	for {
		// Minta Task (Pull)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		task, err := c.GetTask(ctx, &pb.WorkerInfo{WorkerId: workerID, CpuCores: 4})
		cancel()

		if err != nil {
			log.Printf("Error getting task (Master down?): %v. Retrying...", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if task.NoMoreWork {
			log.Println("No more work from Master. Job Done or Password Found!")
			break
		}

		// Proses Cracking
		log.Printf("Processing chunk: %d - %d", task.StartIndex, task.EndIndex)
		found, passwd := bruteForceMD5(task.StartIndex, task.EndIndex, task.TargetHash)

		// Lapor Hasil
		_, err = c.ReportResult(context.Background(), &pb.CrackResult{
			TaskId:        task.TaskId,
			WorkerId:      workerID,
			Success:       found,
			FoundPassword: passwd,
		})

		if err != nil {
			log.Printf("Failed to report result: %v", err)
		}

		if found {
			log.Println("I FOUND THE PASSWORD! Exiting...")
			break
		}
	}
}

// Fungsi sederhana brute force angka (sesuai demo)
// Di dunia nyata, ini akan mencoba wordlist atau kombinasi karakter
func bruteForceMD5(start, end int64, targetHash string) (bool, string) {
	for i := start; i < end; i++ {
		// Konversi angka ke string (misal: "12345")
		candidate := fmt.Sprintf("%05d", i)

		// Hash MD5
		hash := md5.Sum([]byte(candidate))
		hashString := hex.EncodeToString(hash[:])

		if hashString == targetHash {
			return true, candidate
		}
	}
	return false, ""
}
