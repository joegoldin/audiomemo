package record

import (
	"fmt"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
	"maps"
	"slices"
	"strings"
)

// SourceRole reports provenance, not a guess at the identity of a speaker.
func SourceRole(c *config.Config, d Device, inventory []Device) (string, string, error) {
	var explicit string
	for _, key := range slices.Sorted(maps.Keys(c.DeviceRoles)) {
		role := c.DeviceRoles[key]
		raw := key
		if v, ok := c.Devices[key]; ok {
			raw = v
		}
		if ResolveDeviceName(raw, inventory) != d.Name || role == "auto" {
			continue
		}
		if role != "us" && role != "them" {
			return "", "", fmt.Errorf("invalid device_roles.%s: expected us, them or auto", key)
		}
		if explicit != "" && explicit != role {
			return "", "", fmt.Errorf("conflicting roles for %q; reset redundant assignments with device role <name> auto", d.Name)
		}
		explicit = role
	}
	if explicit != "" {
		return explicit, "explicit", nil
	}
	if d.IsMonitor || d.Name == SystemAudioDevice {
		return "them", "system audio", nil
	}
	def := c.Record.Device
	if v, ok := c.Devices[def]; ok {
		def = v
	}
	if _, group := c.DeviceGroups[c.Record.Device]; !group && def != "" {
		for _, candidate := range inventory {
			if candidate.Name == ResolveDeviceName(def, inventory) && !candidate.IsMonitor && candidate.Name != SystemAudioDevice {
				if d.Name == candidate.Name {
					return "us", "configured mic", nil
				}
				return "them", "other input", nil
			}
		}
	}
	if d.IsDefault {
		return "us", "system default mic", nil
	}
	return "them", "other input", nil
}
func RoleLabel(c *config.Config, d Device, inventory []Device) string {
	role, reason, err := SourceRole(c, d, inventory)
	if err != nil {
		return err.Error()
	}
	return strings.ToUpper(role[:1]) + role[1:] + " (" + reason + ")"
}
func ResolveSources(c *config.Config, name string, inventory []Device) ([]sessiontypes.Source, error) {
	if name == "" {
		return nil, fmt.Errorf("select a meeting device or group with -D")
	}
	raws, err := c.ResolveDevice(name)
	if err != nil {
		return nil, err
	}
	var out []sessiontypes.Source
	seen := map[string]bool{}
	for _, raw := range raws {
		key := ResolveDeviceName(raw, inventory)
		var found *Device
		for i := range inventory {
			d := &inventory[i]
			if key == d.Name || key == "default" && d.IsDefault {
				if found != nil {
					return nil, fmt.Errorf("ambiguous device %q", raw)
				}
				found = d
			}
		}
		if found == nil {
			return nil, fmt.Errorf("device %q not present", raw)
		}
		if seen[found.Name] {
			continue
		}
		seen[found.Name] = true
		role, reason, err := SourceRole(c, *found, inventory)
		if err != nil {
			return nil, err
		}
		alias := ""
		for _, a := range slices.Sorted(maps.Keys(c.Devices)) {
			if ResolveDeviceName(c.Devices[a], inventory) == found.Name {
				alias = a
				break
			}
		}
		out = append(out, sessiontypes.Source{ID: fmt.Sprintf("%03d", len(out)+1), Device: found.Name, Alias: alias, Role: role, Reason: reason})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty device group %q", name)
	}
	return out, nil
}
