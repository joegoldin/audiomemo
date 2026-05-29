package record

import (
	"os/exec"
	"regexp"
	"strings"
)

// fuzzyMatchThreshold is the minimum Jaccard similarity required to accept a
// fuzzy device substitution. Empirically, drifts like "HiFi__Line1__sink" ->
// "HiFi__Line__sink" score ~0.83 while unrelated devices stay below 0.5.
const fuzzyMatchThreshold = 0.7

// fuzzyMatchMargin is the minimum lead the best candidate must have over the
// runner-up before we trust an auto-substitution. Prevents picking the wrong
// device when two siblings look equally close.
const fuzzyMatchMargin = 0.05

type Device struct {
	Name        string
	Description string
	IsDefault   bool
	IsMonitor   bool
}

var devicePattern = regexp.MustCompile(`^\s+(\*?)\s*(\S+)\s+\[(.+)\]`)

func ParseDeviceList(output string) []Device {
	var devices []Device
	for _, line := range strings.Split(output, "\n") {
		m := devicePattern.FindStringSubmatch(line)
		if len(m) < 4 {
			continue
		}
		name := m[2]
		devices = append(devices, Device{
			IsDefault:   m[1] == "*",
			Name:        name,
			Description: m[3],
			IsMonitor:   strings.HasSuffix(name, ".monitor"),
		})
	}
	return devices
}

// ResolveDeviceName resolves a device identifier to a raw PulseAudio name.
// If the name already matches a known raw device name, it's returned as-is.
// If it matches a device description (pretty name), the corresponding raw name
// is returned. Otherwise the name is returned unchanged.
func ResolveDeviceName(name string, devices []Device) string {
	for _, d := range devices {
		if d.Name == name {
			return name
		}
	}
	for _, d := range devices {
		if d.Description == name {
			return d.Name
		}
	}
	return name
}

// ResolveDeviceNames resolves a slice of device identifiers, mapping any
// pretty/description names to their raw PulseAudio equivalents.
func ResolveDeviceNames(names []string, devices []Device) []string {
	resolved := make([]string, len(names))
	for i, n := range names {
		resolved[i] = ResolveDeviceName(n, devices)
	}
	return resolved
}

// HasDevice reports whether the given raw device name exists in the list.
func HasDevice(name string, devices []Device) bool {
	for _, d := range devices {
		if d.Name == name {
			return true
		}
	}
	return false
}

var fuzzyTokenSplit = regexp.MustCompile(`[._\-]+`)

// fuzzyTokens splits a device identifier into normalized tokens for similarity
// scoring. Empty segments produced by adjacent separators (e.g. `__`) are
// dropped and tokens are lowercased.
func fuzzyTokens(name string) []string {
	parts := fuzzyTokenSplit.Split(name, -1)
	out := parts[:0]
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, strings.ToLower(p))
	}
	return out
}

func jaccardSimilarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	aSet := make(map[string]bool, len(a))
	for _, t := range a {
		aSet[t] = true
	}
	bSet := make(map[string]bool, len(b))
	for _, t := range b {
		bSet[t] = true
	}
	inter := 0
	for t := range bSet {
		if aSet[t] {
			inter++
		}
	}
	union := len(aSet) + len(bSet) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// devicePrefix returns the substring before the first '.' (e.g. "alsa_input",
// "alsa_output"). Used to constrain fuzzy matches to the same device family so
// we never substitute a sink for a source.
func devicePrefix(name string) string {
	i := strings.IndexByte(name, '.')
	if i < 0 {
		return name
	}
	return name[:i]
}

// FuzzyMatchDevice finds the device whose raw name most closely matches the
// configured name. Intended for self-healing when a stored device identifier
// drifts under our feet — e.g. PulseAudio renames "HiFi__Line1__sink" to
// "HiFi__Line__sink" after a profile change.
//
// Returns the matched device and true only when:
//   - Same direction: prefix family ("alsa_input"/"alsa_output"/...) matches
//     and the .monitor suffix (encoded as IsMonitor) agrees
//   - Jaccard similarity over alphanumeric tokens >= fuzzyMatchThreshold
//   - The best candidate leads the runner-up by >= fuzzyMatchMargin
//
// Callers should already have verified that the configured name isn't an
// exact match (HasDevice == false) before reaching for this.
func FuzzyMatchDevice(name string, devices []Device) (Device, bool) {
	nameTokens := fuzzyTokens(name)
	if len(nameTokens) == 0 {
		return Device{}, false
	}
	targetMonitor := strings.HasSuffix(name, ".monitor")
	targetPrefix := devicePrefix(name)

	var best Device
	bestScore := 0.0
	secondScore := 0.0
	for _, d := range devices {
		if d.Name == "" {
			continue
		}
		if d.IsMonitor != targetMonitor {
			continue
		}
		if devicePrefix(d.Name) != targetPrefix {
			continue
		}
		score := jaccardSimilarity(nameTokens, fuzzyTokens(d.Name))
		if score > bestScore {
			secondScore = bestScore
			bestScore = score
			best = d
		} else if score > secondScore {
			secondScore = score
		}
	}
	if bestScore < fuzzyMatchThreshold {
		return Device{}, false
	}
	if bestScore-secondScore < fuzzyMatchMargin {
		return Device{}, false
	}
	return best, true
}

func ListDevices() ([]Device, error) {
	inputFmt := InputFormat()
	cmd := exec.Command("ffmpeg", "-sources", inputFmt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	return ParseDeviceList(string(out)), nil
}
