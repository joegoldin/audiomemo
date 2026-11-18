package desktopservice

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func owner(fi os.FileInfo) uint32 { return fi.Sys().(*syscall.Stat_t).Uid }

func defaultRuntimeParent() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return "/tmp"
}

func peerUID(fd int) (uint32, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}
