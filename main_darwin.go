package main

import (
	"os"
	"runtime"
)

// Finder and launchd do not inherit the Nix CLI wrapper's helper path.
var desktopToolPath string

// AppKit must run on the initial OS thread. Locking in the daemon's Run
// method alone is too late: Go can migrate main during command dispatch.
func init() {
	runtime.LockOSThread()
	if desktopToolPath != "" {
		path := desktopToolPath
		if old := os.Getenv("PATH"); old != "" {
			path += string(os.PathListSeparator) + old
		}
		_ = os.Setenv("PATH", path)
	}
}
