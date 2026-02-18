package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "audiotools",
	Short: "Audio recording and transcription tools",
	Long:  "A CLI toolkit for recording audio and transcribing it using local or cloud backends.",
}

func init() {
	rootCmd.AddCommand(recordCmd)
	rootCmd.AddCommand(transcribeCmd)
}

func ExecuteRoot() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
