package record

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// muteSourceOutputFn is the mute action, indirected so tests can observe it.
var muteSourceOutputFn = muteSourceOutput

// findSourceOutputsByPID returns every PulseAudio source-output belonging to a
// PID. ffmpeg opens one source-output per `-i` input, so a device-group
// recording owns several under a single PID and all of them must be muted
// together.
func findSourceOutputsByPID(pid int) ([]int, error) {
	out, err := exec.Command("pactl", "list", "source-outputs").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pactl list source-outputs: %w", err)
	}
	ids := parseSourceOutputIDs(string(out), pid)
	if len(ids) == 0 {
		return nil, fmt.Errorf("no source-output found for PID %d", pid)
	}
	return ids, nil
}

// parseSourceOutputIDs extracts the IDs of all source-outputs whose
// application.process.id matches pid, in the order pactl reports them.
func parseSourceOutputIDs(pactlOutput string, pid int) []int {
	pidStr := strconv.Itoa(pid)
	var ids []int
	currentIndex := -1
	for _, line := range strings.Split(pactlOutput, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Source Output #") {
			idx, err := strconv.Atoi(strings.TrimPrefix(line, "Source Output #"))
			if err == nil {
				currentIndex = idx
			} else {
				currentIndex = -1
			}
			continue
		}
		if currentIndex >= 0 &&
			strings.Contains(line, "application.process.id") &&
			strings.Contains(line, `"`+pidStr+`"`) {
			ids = append(ids, currentIndex)
			// Guard against a repeated property line re-adding the same ID.
			currentIndex = -1
		}
	}
	return ids
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
