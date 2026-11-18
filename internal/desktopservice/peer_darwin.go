package desktopservice

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func owner(fi os.FileInfo) uint32 { return fi.Sys().(*syscall.Stat_t).Uid }

func defaultRuntimeParent() string { return os.TempDir() }

func peerUID(fd int) (uint32, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	if cred.Version != 0 {
		return 0, errors.New("unrecognized Darwin peer credential version")
	}
	return cred.Uid, nil
}
