package transcribe

import (
	"bytes"
	"io"
	"testing"
)

func TestSidePreviewDemultiplexesWithoutRetainingLiveOnlyAudio(t *testing.T) {
	stereo := []byte{1, 2, 11, 12, 3, 4, 13, 14}
	for _, keep := range []bool{false, true} {
		var us, them, retained bytes.Buffer
		var save io.Writer
		if keep {
			save = &retained
		}
		if err := feedSides(bytes.NewReader(stereo), [2]io.Writer{&us, &them}, save); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(us.Bytes(), []byte{1, 2, 3, 4}) || !bytes.Equal(them.Bytes(), []byte{11, 12, 13, 14}) {
			t.Fatalf("crossed channels: Us=%v Them=%v", us.Bytes(), them.Bytes())
		}
		if keep && !bytes.Equal(stereo, retained.Bytes()) || !keep && retained.Len() != 0 {
			t.Fatalf("keep=%v retained=%v", keep, retained.Bytes())
		}
	}
}
