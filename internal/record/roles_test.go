package record

import (
	"github.com/joegoldin/audiomemo/internal/config"
	"testing"
)

func TestMeetingRoles(t *testing.T) {
	c := config.Default()
	c.Devices = map[string]string{"mic": "m", "desk": "s", "also": "m"}
	c.DeviceGroups["call"] = []string{"mic", "desk", "also"}
	ds := []Device{{Name: "m", IsDefault: true}, {Name: "s", IsMonitor: true}}
	for _, def := range []string{"mic", "call", "missing"} {
		c.Record.Device = def
		src, e := ResolveSources(c, "call", ds)
		if e != nil || len(src) != 2 || src[0].Role != "us" || src[1].Role != "them" {
			t.Fatalf("%v %v", src, e)
		}
	}
	c.SetDeviceRole("desk", "us")
	src, e := ResolveSources(c, "call", ds)
	if e != nil || src[1].Reason != "explicit" {
		t.Fatal(src, e)
	}
	c.SetDeviceRole("mic", "us")
	c.SetDeviceRole("also", "them")
	if _, e := ResolveSources(c, "call", ds); e == nil {
		t.Fatal("conflicting assignment accepted")
	}
}
