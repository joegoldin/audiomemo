package cmd

import (
	"fmt"
	"os"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/tui"
)

// maybeOnboard runs the first-time onboarding TUI if the config indicates
// setup hasn't been completed yet. With no interactive terminal to run it on it
// is skipped rather than blocking: a headless or piped run cannot answer it,
// and recording can still proceed against the default device.
func maybeOnboard(cfg *config.Config, configPath string, ui tuiTarget) error {
	if !cfg.NeedsOnboarding() {
		return nil
	}
	if !ui.Available {
		fmt.Fprintln(os.Stderr, "Skipping first-run setup: no interactive terminal (run `audiomemo device` to configure).")
		return nil
	}
	_, err := tui.RunOnboarding(cfg, configPath, ui.Options()...)
	return err
}
