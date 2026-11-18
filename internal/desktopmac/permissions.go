package desktopmac

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// DescribeSystemAudioError classifies errors from the native Core Audio tap
// only. Core Audio has no public TCC preflight for system-audio-only capture;
// a Screen Recording check is not a substitute. Keep unrelated tap errors intact.
func DescribeSystemAudioError(err error) error {
	if err == nil || errors.Is(err, ErrSystemAudioDenied) {
		return err
	}
	// The tap adapter currently reports the numeric OSStatus as text.
	_, tail, ok := strings.Cut(err.Error(), "Core Audio status ")
	if !ok {
		return err
	}
	number, _, _ := strings.Cut(tail, ".")
	status, parseErr := strconv.ParseInt(number, 10, 32)
	const noPermission = '!'<<24 | 'h'<<16 | 'o'<<8 | 'g' // kAudioDevicePermissionsError
	if parseErr == nil && status == noPermission {
		return fmt.Errorf("%w: %w", ErrSystemAudioDenied, err)
	}
	return err
}
