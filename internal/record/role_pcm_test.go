package record

import (
	"encoding/binary"
	"github.com/joegoldin/audiomemo/internal/config"
	"os/exec"
	"testing"
)

func TestScribeRoleGraphPreservesChannelsAndLongerSide(t *testing.T) {
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	cfg := config.Default()
	ds := []Device{{Name: "local", IsDefault: true}, {Name: "remote", IsMonitor: true}}
	roles, e := RecordingRoles(cfg, []string{"remote", "local"}, ds)
	if e != nil {
		t.Fatal(e)
	}
	g, m, e := roleGraph(roles)
	if e != nil {
		t.Fatal(e)
	}
	args := []string{"-v", "error", "-f", "lavfi", "-i", "aevalsrc=0.5:s=16000:d=0.1", "-f", "lavfi", "-i", "aevalsrc=-0.25:s=16000:d=0.2", "-filter_complex", g + m + "amix=inputs=2,anullsink", "-map", "[roles]", "-f", "s16le", "pipe:1"}
	pcm, e := exec.Command(ff, args...).Output()
	if e != nil {
		t.Fatal(e)
	}
	if len(pcm) != 3200*4 {
		t.Fatal(len(pcm))
	}
	for _, i := range []int{400, 2400} {
		us := int16(binary.LittleEndian.Uint16(pcm[4*i:]))
		them := int16(binary.LittleEndian.Uint16(pcm[4*i+2:]))
		if us >= 0 || i == 400 && them <= 0 || i == 2400 && them != 0 {
			t.Fatalf("frame %d: %d/%d", i, us, them)
		}
	}
	ds[0].IsDefault = false
	roles, e = RecordingRoles(cfg, []string{"local", "remote"}, ds)
	if e != nil || roles != nil {
		t.Fatalf("unexpected one-sided labels: %v, %v", roles, e)
	}
}
