package master

import (
	"fmt"
	"log"
	"strings"

	"cracker/Common/console"
)

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
