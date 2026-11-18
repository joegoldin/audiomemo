package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newMeetingCommand()) }

func newMeetingCommand() *cobra.Command {
	command := &cobra.Command{Use: "meeting", Short: "Capture and transcribe a multitrack meeting"}
	var device, print string
	var wait bool
	start := &cobra.Command{Use: "start", Short: "Start capturing a meeting", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := desktopRequest(cmd, sessiontypes.Request{Action: "meeting.start", Device: device})
			if err != nil {
				return err
			}
			if r.Status.Capture == "starting" {
				fmt.Fprintln(cmd.ErrOrStderr(), "Meeting admitted; waiting for audio sources.")
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), r.ID)
			return err
		},
	}
	start.Flags().StringVarP(&device, "device", "D", "", "input group")
	stop := &cobra.Command{Use: "stop", Short: "Stop the meeting and output its bundle path", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if print != "path" && print != "text" && print != "none" {
				return fmt.Errorf("invalid --print %q: expected path, text, or none", print)
			}
			r, err := desktopRequest(cmd, sessiontypes.Request{Action: "meeting.stop", Wait: wait, Print: print})
			if err != nil {
				if r.Path != "" {
					return fmt.Errorf("%w (meeting bundle: %s)", err, r.Path)
				}
				return err
			}
			switch print {
			case "path":
				_, err = fmt.Fprintln(cmd.OutOrStdout(), r.Path)
			case "text":
				if r.Text != "" {
					text := r.Text
					if !strings.HasSuffix(text, "\n") {
						text += "\n"
					}
					_, err = fmt.Fprint(cmd.OutOrStdout(), text)
				}
			}
			return err
		},
	}
	stop.Flags().BoolVar(&wait, "wait", false, "wait for finalization of the stopped meeting")
	stop.Flags().StringVar(&print, "print", "path", "output path, text (implies --wait), or none")
	retry := &cobra.Command{Use: "retry <bundle>", Short: "Queue finalization of a closed meeting bundle", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bundle, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			_, err = desktopRequest(cmd, sessiontypes.Request{Action: "meeting.retry", Bundle: bundle})
			return err
		},
	}
	command.AddCommand(start, stop, retry)
	return command
}
