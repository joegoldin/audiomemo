package cmd

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/joegoldin/audiomemo/internal/desktopservice"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"github.com/spf13/cobra"
)

type daemonFactory func(ctx context.Context, configPath string) (desktopservice.Options, error)

var makeDaemon daemonFactory

// SetDaemonFactory installs the application's real capture/delivery wiring.
// Call it once on startup before executing the command tree.
func SetDaemonFactory(factory func(context.Context, string) (desktopservice.Options, error)) {
	makeDaemon = factory
}

func init() {
	rootCmd.AddCommand(newDaemonCommand(func(ctx context.Context, path string) (desktopservice.Options, error) {
		if makeDaemon == nil {
			return desktopservice.Options{}, errors.New("desktop daemon is not configured in this build")
		}
		return makeDaemon(ctx, path)
	}), newQuitCommand())
}

func newDaemonCommand(factory daemonFactory) *cobra.Command {
	var configPath string
	var headless bool
	command := &cobra.Command{
		Use: "daemon", Short: "Run the desktop control daemon in the foreground", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := desktopSignalContext(cmd.Context())
			defer stop()
			opts, err := factory(ctx, configPath)
			if err != nil {
				return err
			}
			if headless {
				opts.Desktop = nil
			}
			return desktopservice.Run(ctx, opts)
		},
	}
	command.Flags().StringVarP(&configPath, "config", "c", "", "configuration file")
	command.Flags().BoolVar(&headless, "headless", false, "run without a desktop adapter")
	return command
}

func newQuitCommand() *cobra.Command {
	return &cobra.Command{
		Use: "quit", Short: "Stop the desktop daemon gracefully", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := desktopRequest(cmd, sessiontypes.Request{Action: "quit"})
			return err
		},
	}
}

func desktopSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func desktopRequest(cmd *cobra.Command, request sessiontypes.Request) (sessiontypes.Reply, error) {
	ctx, stop := desktopSignalContext(cmd.Context())
	defer stop()
	request.Version = sessiontypes.Version
	return (desktopservice.Client{}).Do(ctx, request)
}
