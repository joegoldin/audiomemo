package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// version is stamped at link time (-X); a plain `go build` reports dev.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:     "audiomemo",
	Short:   "Audio recording and transcription tools",
	Long:    "A CLI toolkit for recording audio and transcribing it using local or cloud backends.",
	Version: version,
	// A runtime failure is not a usage mistake, and printing the flag list
	// under it buries the one line that matters. execute prints the error
	// itself, once, in the program's own name.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(recordCmd)
	rootCmd.AddCommand(scribeCmd)
	rootCmd.AddCommand(transcribeCmd)
	rootCmd.AddCommand(deviceCmd)
}

func ExecuteRoot() {
	execute()
}

// execute runs the command tree and turns an error into the conventional
// "name: message" on stderr and exit status 1. The name is argv[0]'s, so the
// rect and transcribe symlinks speak for themselves.
func execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(os.Args[0]), err)
		os.Exit(1)
	}
}
