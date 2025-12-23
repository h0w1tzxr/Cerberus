package main

import (
	"log"
	"os"

	"cracker/Common/console"
	master "cracker/Master/app"
)

func main() {
	if err := master.Run(os.Args[1:]); err != nil {
		log.Fatalf("%s master error: %v", console.TagError(), err)
	}
}
