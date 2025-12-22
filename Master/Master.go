package main

import (
	"log"
	"os"

	master "cracker/Master/app"
)

func main() {
	if err := master.Run(os.Args[1:]); err != nil {
		log.Fatalf("master error: %v", err)
	}
}
