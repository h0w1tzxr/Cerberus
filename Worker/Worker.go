package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"cracker/Common/wordlist"
	pb "cracker/cracker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ganti IP ini dengan IP Laptop Master saat Demo
const MasterAddress = "10.110.1.126:50051"

var wordlistCache = wordlist.NewCache(wordlist.DefaultIndexStride, wordlist.DefaultMaxLineBytes)

const (
	standbyDelay        = 2 * time.Second
	maxStandbyDelay     = 12 * time.Second
	maxErrorDelay       = 15 * time.Second
	progressLogInterval = 2 * time.Second
	progressReportEvery = int64(200)
	progressBarWidth    = 24
	getTaskTimeout      = 4 * time.Second
	statusTickInterval  = 200 * time.Millisecond
)

func main() {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = fmt.Sprintf("%d", time.Now().Unix())
	}
	workerID := "worker-" + hostname // Atau gunakan UUID random

	cpuCores := int32(runtime.NumCPU())
	log.Printf("Worker Starting: %s (cores=%d, master=%s)", workerID, cpuCores, MasterAddress)

	// 1. Connect ke Master via gRPC
	conn, err := grpc.Dial(MasterAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewCrackerServiceClient(conn)

	registerWorker(c, workerID, cpuCores)

	status := newStatusSpinner(statusTickInterval)
	idleDelay := standbyDelay
	errorDelay := standbyDelay

	// 2. Loop Work Stealing
	for {
		// Minta Task (Pull)
		ctx, cancel := context.WithTimeout(context.Background(), getTaskTimeout)
		task, err := c.GetTask(ctx, &pb.WorkerInfo{WorkerId: workerID, CpuCores: cpuCores})
		cancel()

		if err != nil {
			if errorDelay == standbyDelay {
				status.Stop()
				log.Printf("Error getting task: %v", err)
			}
			status.Start("Master unavailable. Retrying...")
			time.Sleep(errorDelay)
			errorDelay = nextDelay(errorDelay, maxErrorDelay)
			continue
		}
		errorDelay = standbyDelay

		if task.DispatchPaused {
			status.Start("Waiting for dispatch...")
			time.Sleep(idleDelay)
			idleDelay = nextDelay(idleDelay, maxStandbyDelay)
			continue
		}

		if task.NoMoreWork {
			status.Start("No work available. Standing by...")
			time.Sleep(idleDelay)
			idleDelay = nextDelay(idleDelay, maxStandbyDelay)
			continue
		}
		status.Stop()
		idleDelay = standbyDelay

		// Proses Cracking
		log.Printf("Processing chunk: %d - %d", task.StartIndex, task.EndIndex)
		progressFn := func(processed, total int64) {
			reportProgress(c, workerID, task.TaskId, processed, total)
		}
		found, passwd, errMsg := processTask(task, progressFn)
		if errMsg != "" {
			log.Printf("Chunk %d-%d failed: %s", task.StartIndex, task.EndIndex, errMsg)
		} else if found {
			log.Printf("Password found for chunk %d-%d: %s", task.StartIndex, task.EndIndex, passwd)
		} else {
			log.Printf("Chunk %d-%d exhausted with no match.", task.StartIndex, task.EndIndex)
		}

		// Lapor Hasil
		err = reportResult(c, &pb.CrackResult{
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
			log.Println("Password found. Standing by...")
			time.Sleep(standbyDelay)
			continue
		}
	}
}

func processTask(task *pb.TaskChunk, onProgress func(processed, total int64)) (bool, string, string) {
	if task.WordlistPath != "" {
		return bruteForceWordlist(task.WordlistPath, task.StartIndex, task.EndIndex, task.TargetHash, normalizeMode(task.Mode), onProgress)
	}
	return bruteForceRange(task.StartIndex, task.EndIndex, task.TotalKeyspace, task.TargetHash, normalizeMode(task.Mode), onProgress)
}

func bruteForceWordlist(path string, start, end int64, targetHash string, mode pb.HashMode, onProgress func(processed, total int64)) (bool, string, string) {
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
	total := end - start
	reporter := newProgressReporter(fmt.Sprintf("Chunk %d-%d", start, end), total, progressLogInterval, onProgress)
	err = reader.ReadRange(start, end, func(line string, lineNumber int64) error {
		processed := lineNumber - start + 1
		if processed%progressReportEvery == 0 || processed == total {
			reporter.MaybeLog(processed)
		}
		if hashCandidate(mode, line) == targetHash {
			found = true
			password = line
			reporter.LogNow(processed)
			return wordlist.ErrStop
		}
		return nil
	})
	if err != nil {
		return false, "", err.Error()
	}
	reporter.LogNow(total)
	return found, password, ""
}

// Fungsi sederhana brute force angka
// Di dunia nyata, ini akan mencoba wordlist atau kombinasi karakter
func bruteForceRange(start, end, totalKeyspace int64, targetHash string, mode pb.HashMode, onProgress func(processed, total int64)) (bool, string, string) {
	width := 1
	if totalKeyspace > 0 {
		width = len(fmt.Sprintf("%d", totalKeyspace-1))
		if width < 1 {
			width = 1
		}
	}
	total := end - start
	reporter := newProgressReporter(fmt.Sprintf("Chunk %d-%d", start, end), total, progressLogInterval, onProgress)
	for i := start; i < end; i++ {
		candidate := fmt.Sprintf("%0*d", width, i)
		if hashCandidate(mode, candidate) == targetHash {
			reporter.LogNow(i - start + 1)
			return true, candidate, ""
		}
		processed := i - start + 1
		if processed%progressReportEvery == 0 || i+1 == end {
			reporter.MaybeLog(processed)
		}
	}
	reporter.LogNow(total)
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

type progressReporter struct {
	label      string
	total      int64
	started    time.Time
	lastLog    time.Time
	interval   time.Duration
	onProgress func(processed, total int64)
}

func newProgressReporter(label string, total int64, interval time.Duration, onProgress func(processed, total int64)) *progressReporter {
	now := time.Now()
	return &progressReporter{
		label:      label,
		total:      total,
		started:    now,
		lastLog:    now.Add(-interval),
		interval:   interval,
		onProgress: onProgress,
	}
}

func (p *progressReporter) MaybeLog(current int64) {
	if p == nil || p.total <= 0 {
		return
	}
	now := time.Now()
	if now.Sub(p.lastLog) < p.interval && current < p.total {
		return
	}
	p.log(current, now)
}

func (p *progressReporter) LogNow(current int64) {
	if p == nil || p.total <= 0 {
		return
	}
	p.log(current, time.Now())
}

func (p *progressReporter) log(current int64, now time.Time) {
	if current < 0 {
		current = 0
	}
	if current > p.total {
		current = p.total
	}
	elapsed := now.Sub(p.started)
	rate := ratePerSecond(current, elapsed)
	eta := estimateETA(p.total-current, rate)
	log.Printf("%s %s rate=%s eta=%s", p.label, formatProgressBar(current, p.total, progressBarWidth), formatRate(rate), formatDuration(eta))
	p.lastLog = now
	if p.onProgress != nil {
		p.onProgress(current, p.total)
	}
}

func formatProgressBar(completed, total int64, width int) string {
	if total <= 0 || width <= 0 {
		return "[no-progress]"
	}
	percent := float64(completed) / float64(total)
	filled := int(percent * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	return fmt.Sprintf("[%s] %6.2f%% (%d/%d)", bar, percent*100, completed, total)
}

func ratePerSecond(processed int64, elapsed time.Duration) float64 {
	if processed <= 0 {
		return 0
	}
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		return 0
	}
	return float64(processed) / seconds
}

func estimateETA(remaining int64, rate float64) time.Duration {
	if remaining <= 0 || rate <= 0 {
		return 0
	}
	seconds := float64(remaining) / rate
	return time.Duration(seconds * float64(time.Second))
}

func formatRate(rate float64) string {
	if rate <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f/s", rate)
}

func formatDuration(duration time.Duration) string {
	if duration <= 0 {
		return "-"
	}
	totalSeconds := int(duration.Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func nextDelay(current, max time.Duration) time.Duration {
	if current <= 0 {
		return max
	}
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func registerWorker(client pb.CrackerServiceClient, workerID string, cpuCores int32) {
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := client.RegisterWorker(ctx, &pb.WorkerInfo{WorkerId: workerID, CpuCores: cpuCores})
		cancel()
		if err == nil {
			return
		}
		log.Printf("Failed to register worker (attempt %d): %v", attempt, err)
		time.Sleep(standbyDelay)
	}
}

func reportProgress(client pb.CrackerServiceClient, workerID, chunkID string, processed, total int64) {
	if chunkID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err := client.ReportProgress(ctx, &pb.ProgressUpdate{
		ChunkId:   chunkID,
		WorkerId:  workerID,
		Processed: processed,
		Total:     total,
	})
	cancel()
	if err != nil {
		log.Printf("Failed to report progress: %v", err)
	}
}

func reportResult(client pb.CrackerServiceClient, result *pb.CrackResult) error {
	if result == nil {
		return nil
	}
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := client.ReportResult(ctx, result)
		cancel()
		if err == nil {
			return nil
		}
		time.Sleep(standbyDelay)
		if attempt == 3 {
			return err
		}
	}
	return nil
}

type statusSpinner struct {
	interval time.Duration
	mu       sync.Mutex
	active   bool
	message  string
	stopCh   chan struct{}
}

func newStatusSpinner(interval time.Duration) *statusSpinner {
	return &statusSpinner{interval: interval}
}

func (s *statusSpinner) Start(message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.active && s.message == message {
		s.mu.Unlock()
		return
	}
	s.stopLocked()
	stopCh := make(chan struct{})
	s.stopCh = stopCh
	s.message = message
	s.active = true
	interval := s.interval
	s.mu.Unlock()

	go func(msg string, stopCh chan struct{}, interval time.Duration) {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		frames := []string{"-", "\\", "|", "/"}
		index := 0
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				select {
				case <-stopCh:
					return
				default:
				}
				fmt.Printf("\r%s %s", frames[index], msg)
				index = (index + 1) % len(frames)
			}
		}
	}(message, stopCh, interval)
}

func (s *statusSpinner) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopLocked()
	s.mu.Unlock()
}

func (s *statusSpinner) stopLocked() {
	if !s.active {
		return
	}
	close(s.stopCh)
	clearStatusLine(s.message)
	s.active = false
	s.stopCh = nil
	s.message = ""
}

func clearStatusLine(message string) {
	if message == "" {
		return
	}
	width := len(message) + 2
	fmt.Printf("\r%s\r", strings.Repeat(" ", width))
}
