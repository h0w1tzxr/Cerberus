package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	"cracker/Common/wordlist"
	pb "cracker/cracker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ganti IP ini dengan IP Laptop Master saat Demo
const MasterAddress = "localhost:50051"

var wordlistCache = wordlist.NewCache(wordlist.DefaultIndexStride, wordlist.DefaultMaxLineBytes)

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

		if task.DispatchPaused {
			log.Println("Dispatch paused. Waiting...")
			time.Sleep(2 * time.Second)
			continue
		}

		if task.NoMoreWork {
			log.Println("No more work from Master. Job Done or Password Found!")
			break
		}

		// Proses Cracking
		log.Printf("Processing chunk: %d - %d", task.StartIndex, task.EndIndex)
		found, passwd, errMsg := processTask(task)

		// Lapor Hasil
		_, err = c.ReportResult(context.Background(), &pb.CrackResult{
			TaskId:        task.TaskId,
			WorkerId:      workerID,
			Success:       found,
			FoundPassword: passwd,
			ErrorMessage:  errMsg,
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

func processTask(task *pb.TaskChunk) (bool, string, string) {
	if task.WordlistPath != "" {
		return bruteForceWordlist(task.WordlistPath, task.StartIndex, task.EndIndex, task.TargetHash, normalizeMode(task.Mode))
	}
	return bruteForceRange(task.StartIndex, task.EndIndex, task.TotalKeyspace, task.TargetHash, normalizeMode(task.Mode))
}

func bruteForceWordlist(path string, start, end int64, targetHash string, mode pb.HashMode) (bool, string, string) {
	index, err := wordlistCache.Get(path)
	if err != nil {
		return false, "", err.Error()
	}
	reader, err := wordlist.NewReader(index)
	if err != nil {
		return false, "", err.Error()
	}
	defer reader.Close()

	var found bool
	var password string
	err = reader.ReadRange(start, end, func(line string, lineNumber int64) error {
		if hashCandidate(mode, line) == targetHash {
			found = true
			password = line
			return wordlist.ErrStop
		}
		return nil
	})
	if err != nil {
		return false, "", err.Error()
	}
	return found, password, ""
}

// Fungsi sederhana brute force angka
// Di dunia nyata, ini akan mencoba wordlist atau kombinasi karakter
func bruteForceRange(start, end, totalKeyspace int64, targetHash string, mode pb.HashMode) (bool, string, string) {
	width := 1
	if totalKeyspace > 0 {
		width = len(fmt.Sprintf("%d", totalKeyspace-1))
		if width < 1 {
			width = 1
		}
	}
	for i := start; i < end; i++ {
		candidate := fmt.Sprintf("%0*d", width, i)
		if hashCandidate(mode, candidate) == targetHash {
			return true, candidate, ""
		}
	}
	return false, "", ""
}

func hashCandidate(mode pb.HashMode, candidate string) string {
	switch normalizeMode(mode) {
	case pb.HashMode_HASH_MODE_SHA256:
		hash := sha256.Sum256([]byte(candidate))
		return hex.EncodeToString(hash[:])
	default:
		hash := md5.Sum([]byte(candidate))
		return hex.EncodeToString(hash[:])
	}
}

func normalizeMode(mode pb.HashMode) pb.HashMode {
	switch mode {
	case pb.HashMode_HASH_MODE_MD5, pb.HashMode_HASH_MODE_SHA256:
		return mode
	default:
		return pb.HashMode_HASH_MODE_MD5
	}
}
