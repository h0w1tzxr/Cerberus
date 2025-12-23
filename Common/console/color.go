package console

const (
	colorReset       = "\x1b[0m"
	colorRed         = "\x1b[31m"
	colorGreen       = "\x1b[32m"
	colorYellow      = "\x1b[33m"
	colorBlue        = "\x1b[34m"
	colorCyan        = "\x1b[36m"
	colorWhite       = "\x1b[37m"
	colorBrightGreen = "\x1b[92m"
)

func TagSuccess() string {
	return ColorSuccess("[+]")
}

func TagError() string {
	return ColorError("[!]")
}

func TagWarn() string {
	return ColorWarn("[*]")
}

func TagInfo() string {
	return ColorInfo("[i]")
}

func ColorWrap(color, text string) string {
	if color == "" || text == "" {
		return text
	}
	return color + text + colorReset
}

func ColorSuccess(text string) string {
	return ColorWrap(colorGreen, text)
}

func ColorSuccessBright(text string) string {
	return ColorWrap(colorBrightGreen, text)
}

func ColorError(text string) string {
	return ColorWrap(colorRed, text)
}

func ColorWarn(text string) string {
	return ColorWrap(colorYellow, text)
}

func ColorInfo(text string) string {
	return ColorWrap(colorCyan, text)
}

func ColorProcessing(text string) string {
	return ColorWrap(colorBlue, text)
}

func ColorMuted(text string) string {
	return ColorWrap(colorWhite, text)
}
