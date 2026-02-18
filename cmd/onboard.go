package cmd

import (
	"github.com/joegoldin/audiotools/internal/config"
	"github.com/joegoldin/audiotools/internal/tui"
)

// maybeOnboard runs the first-time onboarding TUI if the config indicates
// setup hasn't been completed yet.
func maybeOnboard(cfg *config.Config, configPath string) error {
	if !cfg.NeedsOnboarding() {
		return nil
	}
	_, err := tui.RunOnboarding(cfg, configPath)
	return err
}
