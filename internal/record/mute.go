package record

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// findSourceOutputByPID finds the PulseAudio source-output ID for a given PID.
func findSourceOutputByPID(pid int) (int, error) {
	out, err := exec.Command("pactl", "list", "source-outputs").CombinedOutput()
	if err != nil {
		return -1, fmt.Errorf("pactl list source-outputs: %w", err)
	}

	pidStr := strconv.Itoa(pid)
	lines := strings.Split(string(out), "\n")
	currentIndex := -1
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Source Output #") {
			idx, err := strconv.Atoi(strings.TrimPrefix(line, "Source Output #"))
			if err == nil {
				currentIndex = idx
			}
		}
		if strings.Contains(line, "application.process.id") && strings.Contains(line, `"`+pidStr+`"`) {
			return currentIndex, nil
		}
	}
	return -1, fmt.Errorf("no source-output found for PID %d", pid)
}

// muteSourceOutput sets or unsets mute on a PulseAudio source-output.
func muteSourceOutput(sourceOutputID int, mute bool) error {
	val := "0"
	if mute {
		val = "1"
	}
	return exec.Command("pactl", "set-source-output-mute",
		strconv.Itoa(sourceOutputID), val).Run()
}
