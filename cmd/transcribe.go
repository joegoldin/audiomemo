package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var transcribeCmd = &cobra.Command{
	Use:   "transcribe [flags] <file>",
	Short: "Transcribe audio to text",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("transcribe: not yet implemented")
		return nil
	},
}

func ExecuteTranscribe() {
	if err := transcribeCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
