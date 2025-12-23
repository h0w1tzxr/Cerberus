package console

const (
	colorReset  = "\x1b[0m"
	colorRed    = "\x1b[31m"
	colorGreen  = "\x1b[32m"
	colorYellow = "\x1b[33m"
	colorCyan   = "\x1b[36m"
)

func TagSuccess() string {
	return colorGreen + "[+]" + colorReset
}

func TagError() string {
	return colorRed + "[!]" + colorReset
}

func TagWarn() string {
	return colorYellow + "[*]" + colorReset
}

func TagInfo() string {
	return colorCyan + "[i]" + colorReset
}
