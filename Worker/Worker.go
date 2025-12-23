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
	"time"

	"cracker/Common/console"
	"cracker/Common/wordlist"
	pb "cracker/cracker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ganti IP ini dengan IP Laptop Master saat Demo
const MasterAddress = "10.110.1.43:50051"

var wordlistCache = wordlist.NewCache(wordlist.DefaultIndexStride, wordlist.DefaultMaxLineBytes)

const (
	standbyDelay        = 2 * time.Second
	maxStandbyDelay     = 12 * time.Second
	maxErrorDelay       = 15 * time.Second
	progressLogInterval = 2 * time.Second
	progressReportEvery = int64(200)
	getTaskTimeout      = 4 * time.Second
	renderInterval      = time.Second / 30
)

func main() {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = fmt.Sprintf("%d", time.Now().Unix())
	}
	workerID := "worker-" + hostname // Atau gunakan UUID random

	cpuCores := int32(runtime.NumCPU())
	telemetry := newWorkerTelemetry()
	renderer := console.NewStickyRenderer(os.Stdout)
	log.SetOutput(renderer)
	renderLoop := console.NewRenderLoop(renderer, renderInterval, telemetry.StatusLine)
	renderLoop.Start()
	defer renderLoop.Stop()

	telemetry.SetState("starting")
	logInfo("Worker Starting: %s (cores=%d, master=%s)", workerID, cpuCores, MasterAddress)

	// 1. Connect ke Master via gRPC
	telemetry.SetState("connecting")
	conn, err := grpc.Dial(MasterAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logError("Did not connect: %v", err)
		return
	}
	defer conn.Close()
	c := pb.NewCrackerServiceClient(conn)

	registerWorker(c, workerID, cpuCores)

	idleDelay := standbyDelay
	errorDelay := standbyDelay

	// 2. Loop Work Stealing
	for {
		// Minta Task (Pull)
		telemetry.SetState("fetching")
		ctx, cancel := context.WithTimeout(context.Background(), getTaskTimeout)
		task, err := c.GetTask(ctx, &pb.WorkerInfo{WorkerId: workerID, CpuCores: cpuCores})
		cancel()

		if err != nil {
			if errorDelay == standbyDelay {
				logError("Error getting task: %v", err)
			}
			telemetry.SetState("master-down")
			time.Sleep(errorDelay)
			errorDelay = nextDelay(errorDelay, maxErrorDelay)
			continue
		}
		errorDelay = standbyDelay

		if task.DispatchPaused {
			telemetry.SetState("dispatch-paused")
			time.Sleep(idleDelay)
			idleDelay = nextDelay(idleDelay, maxStandbyDelay)
			continue
		}

		if task.NoMoreWork {
			telemetry.SetState("idle")
			time.Sleep(idleDelay)
			idleDelay = nextDelay(idleDelay, maxStandbyDelay)
			continue
		}
		idleDelay = standbyDelay

		// Proses Cracking
		telemetry.StartChunk(task.TaskId, task.EndIndex-task.StartIndex)
		logInfo("Processing chunk: %d - %d", task.StartIndex, task.EndIndex)
		progressFn := func(processed, total int64) {
			telemetry.UpdateProgress(processed, total)
			reportProgress(c, workerID, task.TaskId, processed, total)
		}
		found, passwd, errMsg, stats := processTask(task, progressFn)
		avgRate := 0.0
		if stats.duration > 0 && stats.processed > 0 {
			avgRate = float64(stats.processed) / stats.duration.Seconds()
		}
		telemetry.FinishChunk(stats.processed, stats.total)
		if errMsg != "" {
			logError("Chunk %d-%d failed: %s (duration=%s avg=%s)", task.StartIndex, task.EndIndex, errMsg, formatDuration(stats.duration), console.FormatHashRate(avgRate))
		} else if found {
			logSuccess("Password found for chunk %d-%d: %s (duration=%s avg=%s)", task.StartIndex, task.EndIndex, passwd, formatDuration(stats.duration), console.FormatHashRate(avgRate))
		} else {
			logWarn("Chunk %d-%d exhausted with no match (duration=%s avg=%s)", task.StartIndex, task.EndIndex, formatDuration(stats.duration), console.FormatHashRate(avgRate))
		}

		// Lapor Hasil
		telemetry.SetState("reporting")
		err = reportResult(c, &pb.CrackResult{
			TaskId:        task.TaskId,
			WorkerId:      workerID,
			Success:       found,
			FoundPassword: passwd,
			ErrorMessage:  errMsg,
			Processed:     stats.processed,
			Total:         stats.total,
			DurationMs:    stats.duration.Milliseconds(),
			AvgRate:       avgRate,
		})

		if err != nil {
			logWarn("Failed to report result: %v", err)
		}

		telemetry.SetState("idle")
		if found {
			logInfo("Password found. Standing by...")
			time.Sleep(standbyDelay)
			continue
		}
	}
}

type chunkStats struct {
	processed int64
	total     int64
	duration  time.Duration
}

func processTask(task *pb.TaskChunk, onProgress func(processed, total int64)) (bool, string, string, chunkStats) {
	if task.WordlistPath != "" {
		return bruteForceWordlist(task.WordlistPath, task.StartIndex, task.EndIndex, task.TargetHash, normalizeMode(task.Mode), onProgress)
	}
	return bruteForceRange(task.StartIndex, task.EndIndex, task.TotalKeyspace, task.TargetHash, normalizeMode(task.Mode), onProgress)
}

func bruteForceWordlist(path string, start, end int64, targetHash string, mode pb.HashMode, onProgress func(processed, total int64)) (bool, string, string, chunkStats) {
	index, err := wordlistCache.Get(path)
	if err != nil {
		return false, "", err.Error(), chunkStats{}
	}
	reader, err := wordlist.NewReader(index)
	if err != nil {
		return false, "", err.Error(), chunkStats{}
	}
	defer reader.Close()

	var found bool
	var password string
	total := end - start
	var processed int64
	reporter := newProgressReporter(fmt.Sprintf("Chunk %d-%d", start, end), total, progressLogInterval, onProgress)
	started := time.Now()
	err = reader.ReadRange(start, end, func(line string, lineNumber int64) error {
		processed = lineNumber - start + 1
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
	stats := chunkStats{processed: processed, total: total, duration: time.Since(started)}
	if err != nil {
		return false, "", err.Error(), stats
	}
	reporter.LogNow(total)
	return found, password, "", stats
}

// Fungsi sederhana brute force angka
// Di dunia nyata, ini akan mencoba wordlist atau kombinasi karakter
func bruteForceRange(start, end, totalKeyspace int64, targetHash string, mode pb.HashMode, onProgress func(processed, total int64)) (bool, string, string, chunkStats) {
	width := 1
	if totalKeyspace > 0 {
		width = len(fmt.Sprintf("%d", totalKeyspace-1))
		if width < 1 {
			width = 1
		}
	}
	total := end - start
	reporter := newProgressReporter(fmt.Sprintf("Chunk %d-%d", start, end), total, progressLogInterval, onProgress)
	started := time.Now()
	var processed int64
	for i := start; i < end; i++ {
		candidate := fmt.Sprintf("%0*d", width, i)
		if hashCandidate(mode, candidate) == targetHash {
			reporter.LogNow(i - start + 1)
			processed = i - start + 1
			return true, candidate, "", chunkStats{processed: processed, total: total, duration: time.Since(started)}
		}
		processed = i - start + 1
		if processed%progressReportEvery == 0 || i+1 == end {
			reporter.MaybeLog(processed)
		}
	}
	reporter.LogNow(total)
	return false, "", "", chunkStats{processed: total, total: total, duration: time.Since(started)}
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
	p.lastLog = now
	if p.onProgress != nil {
		p.onProgress(current, p.total)
	}
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
		logWarn("Failed to register worker (attempt %d): %v", attempt, err)
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
		logWarn("Failed to report progress: %v", err)
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
