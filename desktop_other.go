//go:build !linux

package main

import (
	"github.com/joegoldin/audiomemo/internal/desktopmac"
	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
	"runtime"
)

func nativeDesktop() st.Desktop {
	if runtime.GOOS == "darwin" {
		return desktopmac.New()
	}
	return nil
}
