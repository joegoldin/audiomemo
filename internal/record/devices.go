package record

import (
	"os/exec"
	"regexp"
	"strings"
)

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

func ListDevices() ([]Device, error) {
	inputFmt := InputFormat()
	cmd := exec.Command("ffmpeg", "-sources", inputFmt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	return ParseDeviceList(string(out)), nil
}
