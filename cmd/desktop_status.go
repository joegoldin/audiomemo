package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newDesktopStatusCommand()) }

func newDesktopStatusCommand() *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "status", Short: "Show the desktop daemon's current state", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := desktopRequest(cmd, sessiontypes.Request{Action: "status"})
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(r.Status)
			}
			s := r.Status
			line := s.Capture
			if s.Session != "" {
				line += " (" + s.Session + ")"
			}
			if s.Job != "" {
				line += "; background: " + s.JobState + " (" + s.Job + ")"
			}
			if s.Warning != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), s.Warning)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), line)
			return err
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "print a JSON state snapshot")
	return command
}
