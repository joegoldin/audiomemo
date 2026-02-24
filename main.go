package main

import (
	"os"
	"path/filepath"

	"github.com/joegoldin/audiomemo/cmd"
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
