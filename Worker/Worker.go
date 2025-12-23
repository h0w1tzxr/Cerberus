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
	standbyDelay        = 500 * time.Millisecond
	maxStandbyDelay     = 4 * time.Second
	maxErrorDelay       = 10 * time.Second
	progressLogInterval = 750 * time.Millisecond
	progressReportEvery = int64(200)
	getTaskTimeout      = 3 * time.Second
	renderInterval      = time.Second / 30
)

func main() {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = fmt.Sprintf("%d", time.Now().Unix())
	}
	workerID := "worker-" + hostname // Atau gunakan UUID random

	cpuCores := int32(runtime.NumCPU())
	runtime.GOMAXPROCS(int(cpuCores))
	slotCount := int(cpuCores)
	if slotCount < 1 {
		slotCount = 1
	}
	telemetry := newWorkerTelemetry(workerID, MasterAddress, cpuCores, slotCount)
	renderer := console.NewStickyRenderer(os.Stdout)
	log.SetOutput(renderer)
	renderLoop := console.NewRenderLoop(renderer, renderInterval, telemetry.StatusLine)
	renderLoop.Start()
	defer renderLoop.Stop()

	telemetry.SetGlobalState("starting")
	logInfo("Worker Starting: %s (cores=%d, master=%s)", workerID, cpuCores, MasterAddress)

	// 1. Connect ke Master via gRPC
	telemetry.SetGlobalState("connecting")
	conn, err := grpc.Dial(MasterAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logError("Did not connect: %v", err)
		return
	}
	defer conn.Close()
	c := pb.NewCrackerServiceClient(conn)

	if err := registerWorker(c, workerID, cpuCores); err != nil {
		logError("Failed to register worker: %v", err)
		return
	}
	telemetry.SetGlobalState("connected")
	logInfo("Connected to master: %s", MasterAddress)

	tracker := newConnectionTracker(true)
	var wg sync.WaitGroup
	wg.Add(slotCount)
	for i := 0; i < slotCount; i++ {
		slotID := i
		go func() {
			defer wg.Done()
			runWorkerSlot(slotID, c, telemetry, workerID, cpuCores, tracker)
		}()
	}
	wg.Wait()
}

type prefetchResult struct {
	task *pb.TaskChunk
	err  error
}

type connectionTracker struct {
	mu        sync.Mutex
	connected bool
}

func newConnectionTracker(connected bool) *connectionTracker {
	return &connectionTracker{connected: connected}
}

func (c *connectionTracker) MarkConnected(telemetry *workerTelemetry) {
	if c == nil {
		return
	}
	c.mu.Lock()
	wasConnected := c.connected
	c.connected = true
	c.mu.Unlock()
	if !wasConnected {
		logInfo("Master reachable.")
	}
	if telemetry != nil {
		telemetry.SetGlobalState("connected")
	}
}

func (c *connectionTracker) MarkDisconnected(telemetry *workerTelemetry, err error) {
	if c == nil || err == nil {
		return
	}
	c.mu.Lock()
	wasConnected := c.connected
	c.connected = false
	c.mu.Unlock()
	if wasConnected {
		logWarn("Master unreachable: %v", err)
	}
	if telemetry != nil {
		telemetry.SetGlobalState("master-down")
		telemetry.RecordEvent(workerEventWarn, fmt.Sprintf("master unreachable: %v", err))
	}
}

func runWorkerSlot(slotID int, client pb.CrackerServiceClient, telemetry *workerTelemetry, workerID string, cpuCores int32, tracker *connectionTracker) {
	idleDelay := standbyDelay
	errorDelay := standbyDelay
	var prefetched *pb.TaskChunk
	var prefetchedErr error

	for {
		telemetry.SetSlotState(slotID, "fetching")
		var task *pb.TaskChunk
		var err error
		if prefetched != nil || prefetchedErr != nil {
			task = prefetched
			err = prefetchedErr
			prefetched = nil
			prefetchedErr = nil
		} else {
			task, err = getTask(client, workerID, cpuCores)
		}

		if err != nil {
			if tracker != nil {
				tracker.MarkDisconnected(telemetry, err)
			}
			telemetry.SetSlotState(slotID, "master-down")
			time.Sleep(errorDelay)
			errorDelay = nextDelay(errorDelay, maxErrorDelay)
			continue
		}
		if tracker != nil {
			tracker.MarkConnected(telemetry)
		}
		errorDelay = standbyDelay

		if task == nil {
			telemetry.SetSlotState(slotID, "idle")
			time.Sleep(idleDelay)
			idleDelay = nextDelay(idleDelay, maxStandbyDelay)
			continue
		}

		if task.DispatchPaused {
			telemetry.SetSlotState(slotID, "dispatch-paused")
			time.Sleep(idleDelay)
			idleDelay = nextDelay(idleDelay, maxStandbyDelay)
			continue
		}

		if task.NoMoreWork {
			telemetry.SetSlotState(slotID, "idle")
			time.Sleep(idleDelay)
			idleDelay = nextDelay(idleDelay, maxStandbyDelay)
			continue
		}
		idleDelay = standbyDelay

		telemetry.StartChunk(slotID, task.TaskId, task.EndIndex-task.StartIndex)
		prefetchCh := make(chan prefetchResult, 1)
		go func() {
			nextTask, err := getTask(client, workerID, cpuCores)
			prefetchCh <- prefetchResult{task: nextTask, err: err}
		}()

		progressFn := func(processed, total int64) {
			telemetry.UpdateProgress(slotID, processed, total)
			_ = reportProgress(client, workerID, task.TaskId, processed, total)
		}
		found, passwd, errMsg, stats := processTask(task, progressFn)
		avgRate := 0.0
		if stats.duration > 0 && stats.processed > 0 {
			avgRate = float64(stats.processed) / stats.duration.Seconds()
		}
		telemetry.FinishChunk(slotID, stats.processed, stats.total)

		rangeLabel := formatRange(task.StartIndex, task.EndIndex)
		if errMsg != "" {
			telemetry.RecordEvent(workerEventError, fmt.Sprintf("chunk %s failed: %s", rangeLabel, errMsg))
		} else if found {
			msg := fmt.Sprintf("password found for chunk %s: %s", rangeLabel, passwd)
			telemetry.RecordEvent(workerEventSuccess, msg)
			logSuccess("Password found by %s: %s", workerID, passwd)
		}

		telemetry.SetSlotState(slotID, "reporting")
		err = reportResult(client, &pb.CrackResult{
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
			telemetry.RecordEvent(workerEventWarn, fmt.Sprintf("report result failed: %v", err))
		}

		telemetry.SetSlotState(slotID, "idle")
		if found {
			time.Sleep(standbyDelay)
		}

		telemetry.SetSlotState(slotID, "fetching")
		res := <-prefetchCh
		prefetched = res.task
		prefetchedErr = res.err
	}
}

func getTask(client pb.CrackerServiceClient, workerID string, cpuCores int32) (*pb.TaskChunk, error) {
	ctx, cancel := context.WithTimeout(context.Background(), getTaskTimeout)
	task, err := client.GetTask(ctx, &pb.WorkerInfo{WorkerId: workerID, CpuCores: cpuCores})
	cancel()
	return task, err
}

type chunkStats struct {
	processed int64
	total     int64
	duration  time.Duration
}

func processTask(task *pb.TaskChunk, onProgress func(processed, total int64)) (bool, string, string, chunkStats) {
	targetHash := strings.ToLower(strings.TrimSpace(task.TargetHash))
	if task.WordlistPath != "" {
		return bruteForceWordlist(task.WordlistPath, task.StartIndex, task.EndIndex, targetHash, normalizeMode(task.Mode), onProgress)
	}
	return bruteForceRange(task.StartIndex, task.EndIndex, task.TotalKeyspace, targetHash, normalizeMode(task.Mode), onProgress)
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
	reportEvery := reportEveryForTotal(total)
	reporter := newProgressReporter(fmt.Sprintf("Chunk %d-%d", start, end), total, progressLogInterval, onProgress)
	started := time.Now()
	err = reader.ReadRange(start, end, func(line string, lineNumber int64) error {
		processed = lineNumber - start + 1
		if processed%reportEvery == 0 || processed == total {
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
	reportEvery := reportEveryForTotal(total)
	reporter := newProgressReporter(fmt.Sprintf("Chunk %d-%d", start, end), total, progressLogInterval, onProgress)
	started := time.Now()
	var processed int64
	buf := make([]byte, width)
	for i := start; i < end; i++ {
		formatCandidate(buf, width, i)
		if hashCandidateBytes(mode, buf) == targetHash {
			reporter.LogNow(i - start + 1)
			processed = i - start + 1
			return true, string(buf), "", chunkStats{processed: processed, total: total, duration: time.Since(started)}
		}
		processed = i - start + 1
		if processed%reportEvery == 0 || i+1 == end {
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

func hashCandidateBytes(mode pb.HashMode, candidate []byte) string {
	switch normalizeMode(mode) {
	case pb.HashMode_HASH_MODE_SHA256:
		hash := sha256.Sum256(candidate)
		return hex.EncodeToString(hash[:])
	default:
		hash := md5.Sum(candidate)
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

func reportEveryForTotal(total int64) int64 {
	reportEvery := progressReportEvery
	if total > 0 {
		step := total / 100
		if step > reportEvery {
			reportEvery = step
		}
	}
	if reportEvery < 1 {
		return 1
	}
	return reportEvery
}

func formatCandidate(buf []byte, width int, value int64) {
	if width <= 0 {
		width = 1
	}
	if len(buf) < width {
		return
	}
	for i := width - 1; i >= 0; i-- {
		buf[i] = byte('0' + value%10)
		value /= 10
	}
}

func registerWorker(client pb.CrackerServiceClient, workerID string, cpuCores int32) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := client.RegisterWorker(ctx, &pb.WorkerInfo{WorkerId: workerID, CpuCores: cpuCores})
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(standbyDelay)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("registration failed")
	}
	return lastErr
}

func reportProgress(client pb.CrackerServiceClient, workerID, chunkID string, processed, total int64) error {
	if chunkID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err := client.ReportProgress(ctx, &pb.ProgressUpdate{
		ChunkId:   chunkID,
		WorkerId:  workerID,
		Processed: processed,
		Total:     total,
	})
	cancel()
	return err
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

func formatRange(start, end int64) string {
	if end <= start {
		return fmt.Sprintf("%d-%d", start, start)
	}
	return fmt.Sprintf("%d-%d", start, end-1)
}
