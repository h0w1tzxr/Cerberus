package master

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"cracker/Common/console"
)

type leaderboardEntry struct {
	workerID     string
	tasks        int64
	avgTaskDur   time.Duration
	lastTaskDur  time.Duration
	totalTaskDur time.Duration
}

func snapshotLeaderboardLocked(state *masterState) []leaderboardEntry {
	if state == nil {
		return nil
	}
	entries := make([]leaderboardEntry, 0, len(state.workers))
	for _, info := range state.workers {
		if info == nil {
			continue
		}
		avgTaskDur := time.Duration(0)
		if info.CompletedTasks > 0 {
			avgTaskDur = time.Duration(int64(info.TotalTaskDuration) / info.CompletedTasks)
		}
		entries = append(entries, leaderboardEntry{
			workerID:     info.ID,
			tasks:        info.CompletedTasks,
			avgTaskDur:   avgTaskDur,
			lastTaskDur:  info.LastTaskDuration,
			totalTaskDur: info.TotalTaskDuration,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].tasks != entries[j].tasks {
			return entries[i].tasks > entries[j].tasks
		}
		if entries[i].avgTaskDur != entries[j].avgTaskDur {
			if entries[i].avgTaskDur == 0 {
				return false
			}
			if entries[j].avgTaskDur == 0 {
				return true
			}
			return entries[i].avgTaskDur < entries[j].avgTaskDur
		}
		return entries[i].totalTaskDur < entries[j].totalTaskDur
	})
	return entries
}

const (
	leaderboardWorkerWidth  = 12
	leaderboardTasksWidth   = 5
	leaderboardAvgMsWidth   = 12
	leaderboardLastMsWidth  = 13
	leaderboardTotalMsWidth = 14
)

func formatLeaderboard(entries []leaderboardEntry) string {
	var buf bytes.Buffer
	sep := console.SeparatorLine()
	fmt.Fprintf(&buf, "%s\n", sep)
	fmt.Fprintln(&buf, "LEADERBOARD")
	header := fmt.Sprintf("%s %s %s %s %s",
		padRight("WORKER", leaderboardWorkerWidth),
		padLeft("TASKS", leaderboardTasksWidth),
		padLeft("AVG_TASK_MS", leaderboardAvgMsWidth),
		padLeft("LAST_TASK_MS", leaderboardLastMsWidth),
		padLeft("TOTAL_TASK_MS", leaderboardTotalMsWidth),
	)
	fmt.Fprintln(&buf, header)
	for _, entry := range entries {
		workerLabel := padRight(shortLabel(entry.workerID, leaderboardWorkerWidth), leaderboardWorkerWidth)
		taskLabel := padLeft(fmt.Sprintf("%d", entry.tasks), leaderboardTasksWidth)
		avgLabel := padLeft(formatDurationMs(entry.avgTaskDur), leaderboardAvgMsWidth)
		lastLabel := padLeft(formatDurationMs(entry.lastTaskDur), leaderboardLastMsWidth)
		totalLabel := padLeft(formatDurationMs(entry.totalTaskDur), leaderboardTotalMsWidth)
		fmt.Fprintf(&buf, "%s %s %s %s %s\n",
			workerLabel,
			taskLabel,
			avgLabel,
			lastLabel,
			totalLabel,
		)
	}
	fmt.Fprintf(&buf, "%s\n", sep)
	return buf.String()
}

func formatDurationMs(duration time.Duration) string {
	if duration <= 0 {
		return "-"
	}
	return fmt.Sprintf("%dms", duration.Milliseconds())
}
