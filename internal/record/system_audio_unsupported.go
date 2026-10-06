//go:build !darwin || !cgo

package record

import "fmt"

func systemAudioAvailable() bool { return false }

func startSystemAudio() (*systemAudioCapture, error) {
	return nil, fmt.Errorf("system-audio requires macOS 14.2 or later and an audiomemo build with native Core Audio support (cgo)")
}
