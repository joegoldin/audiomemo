package main

import (
	"github.com/joegoldin/audiomemo/internal/desktoplinux"
	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
	"os"
)

func nativeDesktop() st.Desktop {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil
	}
	return desktoplinux.New()
}
