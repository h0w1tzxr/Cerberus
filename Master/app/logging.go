package master

import (
	"fmt"
	"log"

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
