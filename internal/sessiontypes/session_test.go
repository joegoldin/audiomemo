package sessiontypes

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestManifestRoundTrip(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/desktop/session-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var m Manifest
	if e = json.Unmarshal(raw, &m); e != nil {
		t.Fatal(e)
	}
	if m.Version != Version || len(m.Tracks) != 2 || m.Tracks[1].Role != "them" {
		t.Fatal(m)
	}
	out, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(out, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatal(string(out))
	}
}
