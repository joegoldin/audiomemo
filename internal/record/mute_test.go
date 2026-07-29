package record

import (
	"reflect"
	"testing"
)

// Real `pactl list source-outputs` shape: one process can own several
// source-outputs. ffmpeg opens one per `-i` input, so a device-group
// recording has N of them under a single PID.
const twoInputsSamePID = `Source Output #42
	Driver: protocol-native.c
	Owner Module: 12
	Client: 99
	Source: 3
	Properties:
		application.name = "ffmpeg"
		application.process.id = "5150"
		media.name = "record"

Source Output #43
	Driver: protocol-native.c
	Owner Module: 12
	Client: 99
	Source: 7
	Properties:
		application.name = "ffmpeg"
		application.process.id = "5150"
		media.name = "record"
`

func TestParseSourceOutputIDsFindsAllForPID(t *testing.T) {
	got := parseSourceOutputIDs(twoInputsSamePID, 5150)
	want := []int{42, 43}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected all source-outputs for the ffmpeg PID: want %v, got %v", want, got)
	}
}

func TestParseSourceOutputIDsIgnoresOtherPIDs(t *testing.T) {
	const mixed = `Source Output #10
	Properties:
		application.process.id = "111"

Source Output #11
	Properties:
		application.process.id = "5150"

Source Output #12
	Properties:
		application.process.id = "222"

Source Output #13
	Properties:
		application.process.id = "5150"
`
	got := parseSourceOutputIDs(mixed, 5150)
	want := []int{11, 13}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestParseSourceOutputIDsNoMatch(t *testing.T) {
	if got := parseSourceOutputIDs(twoInputsSamePID, 9999); len(got) != 0 {
		t.Errorf("expected no matches for absent PID, got %v", got)
	}
}

func TestParseSourceOutputIDsSingleInput(t *testing.T) {
	const single = `Source Output #7
	Properties:
		application.process.id = "5150"
`
	got := parseSourceOutputIDs(single, 5150)
	want := []int{7}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

// ToggleMute must act on every source-output the recording owns, not just the
// first — otherwise a device-group recording keeps capturing the un-muted
// inputs.
func TestToggleMuteAppliesToAllSourceOutputs(t *testing.T) {
	type call struct {
		id   int
		mute bool
	}
	var calls []call
	restore := muteSourceOutputFn
	muteSourceOutputFn = func(id int, mute bool) error {
		calls = append(calls, call{id, mute})
		return nil
	}
	defer func() { muteSourceOutputFn = restore }()

	r := &Recorder{}
	r.setSourceOutputIDs([]int{42, 43})

	r.ToggleMute()
	if !r.IsMuted() {
		t.Error("expected recorder to report muted")
	}
	want := []call{{42, true}, {43, true}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("mute: want %v, got %v", want, calls)
	}

	calls = nil
	r.ToggleMute()
	if r.IsMuted() {
		t.Error("expected recorder to report unmuted")
	}
	want = []call{{42, false}, {43, false}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("unmute: want %v, got %v", want, calls)
	}
}

func TestToggleMuteWithNoSourceOutputsStillTracksState(t *testing.T) {
	restore := muteSourceOutputFn
	muteSourceOutputFn = func(id int, mute bool) error { return nil }
	defer func() { muteSourceOutputFn = restore }()

	r := &Recorder{}
	r.ToggleMute()
	if !r.IsMuted() {
		t.Error("mute state should flip even before source-outputs are discovered")
	}
}
