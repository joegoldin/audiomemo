package main

import (
	"os"
	"path/filepath"

	"github.com/joegilkes/audiotools/cmd"
)

func main() {
	name := filepath.Base(os.Args[0])
	switch name {
	case "record", "rec":
		cmd.ExecuteRecord()
	case "transcribe":
		cmd.ExecuteTranscribe()
	default:
		cmd.ExecuteRoot()
	}
}
