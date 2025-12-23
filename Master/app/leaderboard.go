package master

import (
	"bytes"
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"cracker/Common/console"
)

type leaderboardEntry struct {
	workerID   string
	rate       float64
	chunks     int64
	tasks      int64
	avgTaskDur time.Duration
	efficiency float64
	duration   time.Duration
	overhead   time.Duration
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
		rate := 0.0
		if info.TotalDuration > 0 && info.TotalProcessed > 0 {
			rate = float64(info.TotalProcessed) / info.TotalDuration.Seconds()
		}
		totalTime := info.TotalDuration + info.TotalOverhead
		efficiency := 0.0
		if totalTime > 0 {
			efficiency = float64(info.TotalDuration) / float64(totalTime)
		}
		avgTaskDur := time.Duration(0)
		if info.CompletedTasks > 0 {
			avgTaskDur = time.Duration(int64(info.TotalTaskDuration) / info.CompletedTasks)
		}
		entries = append(entries, leaderboardEntry{
			workerID:   info.ID,
			rate:       rate,
			chunks:     info.CompletedChunks,
			tasks:      info.CompletedTasks,
			avgTaskDur: avgTaskDur,
			efficiency: efficiency,
			duration:   info.TotalDuration,
			overhead:   info.TotalOverhead,
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
		return entries[i].rate > entries[j].rate
	})
	return entries
}

func formatLeaderboard(taskID string, entries []leaderboardEntry) string {
	if taskID == "" {
		taskID = "-"
	}
	var buf bytes.Buffer
	sep := console.SeparatorLine()
	fmt.Fprintf(&buf, "%s\n", sep)
	fmt.Fprintf(&buf, "leaderboard (task=%s)\n", taskID)
	writer := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "WORKER\tTASKS\tAVG_TASK\tTHROUGHPUT\tCHUNKS\tEFFICIENCY\tCOMPUTE\tOVERHEAD")
	for _, entry := range entries {
		effLabel := fmt.Sprintf("%.1f%%", entry.efficiency*100)
		fmt.Fprintf(writer, "%s\t%d\t%s\t%s\t%d\t%s\t%s\t%s\n",
			entry.workerID,
			entry.tasks,
			formatDuration(entry.avgTaskDur),
			console.FormatHashRate(entry.rate),
			entry.chunks,
			effLabel,
			formatDuration(entry.duration),
			formatDuration(entry.overhead),
		)
	}
	_ = writer.Flush()
	fmt.Fprintf(&buf, "%s\n", sep)
	return buf.String()
}
