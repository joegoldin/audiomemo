package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var recordCmd = &cobra.Command{
	Use:     "record [flags] [filename]",
	Aliases: []string{"rec"},
	Short:   "Record audio from microphone",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("record: not yet implemented")
		return nil
	},
}

func ExecuteRecord() {
	if err := recordCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
