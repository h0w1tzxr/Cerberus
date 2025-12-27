package master

import (
	"bytes"
	"fmt"
	"time"

	"cracker/Common/console"
)

type sessionReport struct {
	tasksProcessed int
	passwordsFound int
	successRate    float64
	totalTime      time.Duration
	topWorker      string
	topShare       float64
}

func formatSessionReportLocked(state *masterState) string {
	if state == nil {
		return ""
	}
	report := buildSessionReportLocked(state)
	if report.tasksProcessed == 0 {
		return ""
	}
	var buf bytes.Buffer
	sep := console.SeparatorLine()
	fmt.Fprintf(&buf, "%s\n", sep)
	fmt.Fprintln(&buf, "SESSION COMPLETE")
	fmt.Fprintf(&buf, "%s\n", sep)
	fmt.Fprintf(&buf, "- Tasks Processed: %d\n", report.tasksProcessed)
	fmt.Fprintf(&buf, "- Passwords Found: %d (Success Rate: %.1f%%)\n", report.passwordsFound, report.successRate)
	fmt.Fprintf(&buf, "- Total Time:      %s\n", formatDuration(report.totalTime))
	if report.topWorker != "" {
		fmt.Fprintf(&buf, "- Top Worker:      %s (%.1f%% contribution)\n", report.topWorker, report.topShare)
	} else {
		fmt.Fprintln(&buf, "- Top Worker:      -")
	}
	fmt.Fprintf(&buf, "%s\n", sep)
	return buf.String()
}

func buildSessionReportLocked(state *masterState) sessionReport {
	report := sessionReport{}
	if state == nil {
		return report
	}
	var (
		earliest time.Time
		latest   time.Time
	)
	for _, task := range state.tasks {
		if task == nil {
			continue
		}
		report.tasksProcessed++
		if task.Found {
			report.passwordsFound++
		}
		start := task.StartedAt
		if start.IsZero() {
			start = task.CreatedAt
		}
		end := task.CompletedAt
		if end.IsZero() {
			end = task.UpdatedAt
		}
		if !start.IsZero() && (earliest.IsZero() || start.Before(earliest)) {
			earliest = start
		}
		if !end.IsZero() && (latest.IsZero() || end.After(latest)) {
			latest = end
		}
	}
	if report.tasksProcessed > 0 {
		report.successRate = float64(report.passwordsFound) / float64(report.tasksProcessed) * 100
	}
	if !earliest.IsZero() && !latest.IsZero() && latest.After(earliest) {
		report.totalTime = latest.Sub(earliest)
	}
	report.topWorker, report.topShare = topWorkerContributionLocked(state)
	return report
}

func topWorkerContributionLocked(state *masterState) (string, float64) {
	if state == nil {
		return "", 0
	}
	var (
		totalProcessed int64
		totalTasks     int64
		topID          string
		topProcessed   int64
		topTasks       int64
	)
	for _, worker := range state.workers {
		if worker == nil {
			continue
		}
		totalProcessed += worker.TotalProcessed
		totalTasks += worker.CompletedTasks
		if worker.TotalProcessed > topProcessed {
			topProcessed = worker.TotalProcessed
			topID = worker.ID
		}
		if worker.CompletedTasks > topTasks {
			topTasks = worker.CompletedTasks
		}
	}
	if topID == "" {
		return "", 0
	}
	if totalProcessed > 0 {
		return topID, float64(topProcessed) / float64(totalProcessed) * 100
	}
	if totalTasks > 0 {
		return topID, float64(topTasks) / float64(totalTasks) * 100
	}
	return topID, 0
}
