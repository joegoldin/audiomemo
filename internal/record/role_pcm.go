package record

import (
	"fmt"
	"strings"

	"github.com/joegoldin/audiomemo/internal/config"
)

// RecordingRoles returns channel assignments only for a genuine two-sided selection.
func RecordingRoles(cfg *config.Config, devices []string, inventory []Device) ([]string, error) {
	if len(devices) < 2 {
		return nil, nil
	}
	roles := make([]string, len(devices))
	seen := map[string]bool{}
	for i, name := range devices {
		var d *Device
		for j := range inventory {
			if inventory[j].Name == name || (name == "default" && inventory[j].IsDefault) {
				d = &inventory[j]
				break
			}
		}
		if d == nil {
			return nil, fmt.Errorf("cannot identify selected source %q", name)
		}
		r, _, err := SourceRole(cfg, *d, inventory)
		if err != nil {
			return nil, err
		}
		seen[r] = true
		roles[i] = r
	}
	if !seen["us"] || !seen["them"] {
		return nil, nil
	}
	return roles, nil
}

// Each input feeds the ordinary listening mix and exactly one side. Pan maps
// explicitly: channel order must not depend on device enumeration order.
func roleGraph(roles []string) (string, string, error) {
	var g, mix strings.Builder
	groups := map[string][]string{}
	for i, r := range roles {
		if r != "us" && r != "them" {
			return "", "", fmt.Errorf("invalid input role %q", r)
		}
		fmt.Fprintf(&g, "[%d:a]asetpts=PTS-STARTPTS,aresample=16000:async=1:first_pts=0,asplit[m%d][r%d];", i, i, i)
		fmt.Fprintf(&mix, "[m%d]", i)
		groups[r] = append(groups[r], fmt.Sprintf("[r%d]", i))
	}
	for _, r := range []string{"us", "them"} {
		ins := groups[r]
		if len(ins) == 0 {
			return "", "", fmt.Errorf("missing %s inputs", r)
		}
		fmt.Fprintf(&g, "%samix=inputs=%d:duration=longest,aformat=channel_layouts=mono[%s];", strings.Join(ins, ""), len(ins), r)
	}
	g.WriteString("[us]pan=stereo|c0=c0|c1=0*c0[l];[them]pan=stereo|c0=0*c0|c1=c0[r];[l][r]amix=inputs=2:duration=longest:normalize=0[roles];")
	return g.String(), mix.String(), nil
}
