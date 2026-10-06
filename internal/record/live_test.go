package record

import (
	"strings"
	"testing"
)

func TestLiveMultiDeviceUsesMixedAudio(t *testing.T) {
	args, err := BuildFFmpegArgsMulti(RecordOpts{Devices: []string{"mic", "monitor"}, Format: "ogg", SampleRate: 48000, Channels: 1, OutputPath: "recording.ogg", LivePCM: true})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "asplit=2[a][b]") {
		t.Fatalf("live audio must branch from the mixed stream: %s", joined)
	}
}
