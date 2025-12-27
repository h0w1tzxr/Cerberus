package master

import (
	"fmt"
	"log"
	"os"
	"strings"

	"cracker/Common/console"
)

var verboseLogging = parseVerboseLogging()

func parseVerboseLogging() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CERBERUS_VERBOSE")))
	return value == "1" || value == "true" || value == "yes"
}

func logInfo(format string, args ...interface{}) {
	log.Printf("%s %s", console.TagInfo(), fmt.Sprintf(format, args...))
}

func logWarn(format string, args ...interface{}) {
	log.Printf("%s %s", console.TagWarn(), fmt.Sprintf(format, args...))
}

func logError(format string, args ...interface{}) {
	log.Printf("%s %s", console.TagError(), fmt.Sprintf(format, args...))
}

func logSuccess(format string, args ...interface{}) {
	log.Printf("%s %s", console.TagSuccess(), fmt.Sprintf(format, args...))
}

func logDebug(format string, args ...interface{}) {
	if !verboseLogging {
		return
	}
	log.Printf("%s %s", console.TagInfo(), fmt.Sprintf(format, args...))
}

func logBlockInfo(block string) {
	logBlock(block)
}

func logBlock(block string) {
	block = strings.TrimRight(block, "\n")
	if block == "" {
		return
	}
	writer := log.Writer()
	if !strings.HasSuffix(block, "\n") {
		block += "\n"
	}
	_, _ = fmt.Fprint(writer, block)
	if renderer, ok := writer.(*console.StickyRenderer); ok {
		_ = renderer.Flush()
	}
}
