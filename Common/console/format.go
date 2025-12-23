package console

import (
	"fmt"
	"strings"
)

type ProgressState string

const (
	ProgressStateProcessing ProgressState = "processing"
	ProgressStateCompleted  ProgressState = "completed"
	ProgressStateStalled    ProgressState = "stalled"
	ProgressStateIdle       ProgressState = "idle"
)

func FormatHashRate(rate float64) string {
	if rate <= 0 {
		return "-"
	}
	switch {
	case rate >= 1e9:
		return fmt.Sprintf("%.2f GH/s", rate/1e9)
	case rate >= 1e6:
		return fmt.Sprintf("%.2f MH/s", rate/1e6)
	case rate >= 1e3:
		return fmt.Sprintf("%.2f kH/s", rate/1e3)
	default:
		return fmt.Sprintf("%.0f H/s", rate)
	}
}

func FormatPercent(processed, total int64) string {
	if total <= 0 {
		return "0%"
	}
	percent := float64(processed) / float64(total) * 100
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return fmt.Sprintf("%.2f%%", percent)
}

func FormatProgressBar(processed, total int64, width int, state ProgressState) string {
	if total <= 0 || width <= 0 {
		return "[no-progress]"
	}
	if processed < 0 {
		processed = 0
	}
	if processed > total {
		processed = total
	}
	percent := float64(processed) / float64(total)
	filled := int(percent * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	color := progressColor(state)
	if color != "" {
		bar = color + bar + colorReset
	}
	return fmt.Sprintf("[%s] %6.2f%%", bar, percent*100)
}

func SeparatorLine() string {
	width, _ := TerminalSize()
	if width <= 0 {
		width = 72
	}
	if width > 1 {
		width--
	}
	return strings.Repeat("-", width)
}

func progressColor(state ProgressState) string {
	switch state {
	case ProgressStateCompleted:
		return colorGreen
	case ProgressStateStalled:
		return colorYellow
	case ProgressStateProcessing:
		return colorBlue
	case ProgressStateIdle:
		return colorWhite
	default:
		return ""
	}
}
