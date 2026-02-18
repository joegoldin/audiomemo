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
}

var devicePattern = regexp.MustCompile(`^\s+(\*?)\s*(\S+)\s+\[(.+)\]`)

func ParseDeviceList(output string) []Device {
	var devices []Device
	for _, line := range strings.Split(output, "\n") {
		m := devicePattern.FindStringSubmatch(line)
		if len(m) < 4 {
			continue
		}
		devices = append(devices, Device{
			IsDefault:   m[1] == "*",
			Name:        m[2],
			Description: m[3],
		})
	}
	return devices
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
