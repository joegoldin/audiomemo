package cmd

import (
	"fmt"
	"strings"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newDictateCommand()) }

func newDictateCommand() *cobra.Command {
	command := &cobra.Command{Use: "dictate", Short: "Control Quick Scribe"}
	for action, desc := range map[string]string{
		"start": "Start dictation", "stop": "Stop and finalize dictation", "toggle": "Start or stop dictation", "cancel": "Discard dictation or cancel its pending result",
	} {
		var device string
		var both bool
		control := &cobra.Command{Use: action, Short: desc, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, err := desktopRequest(cmd, sessiontypes.Request{Action: "dictate." + action, Device: device, Both: both})
				return err
			},
		}
		if action == "start" || action == "toggle" {
			control.Flags().StringVarP(&device, "device", "D", "", "microphone, alias, or input group")
			control.Flags().BoolVar(&both, "both", false, "caption Us and Them from the configured desktop group")
			control.MarkFlagsMutuallyExclusive("device", "both")
		}
		command.AddCommand(control)
	}
	var copyResult bool
	last := &cobra.Command{Use: "last", Short: "Print the last completed Quick Scribe transcript", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if copyResult {
				_, err := desktopRequest(cmd, sessiontypes.Request{Action: "last.copy"})
				return err
			}
			r, err := desktopRequest(cmd, sessiontypes.Request{Action: "last.text"})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(r.Text, "\n"))
			return err
		},
	}
	last.Flags().BoolVarP(&copyResult, "copy", "C", false, "copy the last transcript instead of printing it")
	command.AddCommand(last)
	return command
}
