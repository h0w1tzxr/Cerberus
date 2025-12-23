package console

import "fmt"

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
