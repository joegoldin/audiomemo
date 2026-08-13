package main

import (
	"os"
	"path/filepath"

	"github.com/joegoldin/audiomemo/cmd"
)

func main() {
	name := filepath.Base(os.Args[0])
	switch name {
	case "record", "rect":
		cmd.ExecuteRecord()
	case "recw":
		cmd.ExecuteRecordWhisper()
	case "transcribe":
		cmd.ExecuteTranscribe()
	default:
		cmd.ExecuteRoot()
	}
}
