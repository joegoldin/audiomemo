package meetingfinal

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/joegoldin/audiomemo/internal/meetingcapture"
)

func TestMixAlignsDriftedRatesAndKeepsGaps(t *testing.T) {
	const nominal = 30_000
	const pulseAt = 5200
	dir := t.TempDir()
	seq := func(id string, track int) []meetingcapture.Segment {
		t.Helper()
		var spans []meetingcapture.Segment
		for i := range 20 {
			// Track two ran 400 ppm slow, started late, and lost a full span midway.
			if track == 1 && i == 10 {
				continue
			}
			start := int64(i * nominal)
			length := int64(nominal)
			rate, channels := 8000, 2
			if track == 1 {
				start += 120 + int64(i*12)
				length += 12
				rate = 16000
			}
			frames := int64(rate) * nominal / 1000
			name := filepath.Join(dir, id+"-"+string(rune('a'+i))+".wav")
			f, err := os.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(wavHeader(frames, rate, channels)); err != nil {
				t.Fatal(err)
			}
			if err := f.Truncate(44 + frames*4); err != nil {
				t.Fatal(err)
			}
			// Synthesize an impulse at the same meeting time in independent clocks,
			// in opposite channels so alignment can be measured on the rendered mix.
			position := (int64(i*nominal+pulseAt) - start) * frames / length
			var pulse [4]byte
			binary.LittleEndian.PutUint16(pulse[track*2:], 28000)
			for j := range int64(rate / 100) {
				if _, err := f.WriteAt(pulse[:], 44+(position+j)*4); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			spans = append(spans, meetingcapture.Segment{File: name, StartMS: start, EndMS: start + length, Frames: frames, Rate: rate, Channels: channels})
		}
		return spans
	}
	spans := [][]meetingcapture.Segment{seq("left", 0), seq("right", 1)}
	for _, i := range []int{0, 9, 10, 19} {
		start := int64(i*nominal + pulseAt - 30)
		var out bytes.Buffer
		if err := renderWAV(t.Context(), &out, os.Open, spans, start, start+100, mixRate, 2, 0.4); err != nil {
			t.Fatal(err)
		}
		first := [2]int{-1, -1}
		b := out.Bytes()[44:]
		for frame := range len(b) / 4 {
			for c := range 2 {
				value := int16(binary.LittleEndian.Uint16(b[frame*4+c*2:]))
				if value > 1000 && first[c] == -1 {
					first[c] = frame
				}
				if value > 12000 {
					t.Fatalf("headroom exceeded: %d", value)
				}
			}
		}
		if first[0] < 0 || i == 10 && first[1] != -1 || i != 10 && (first[1] < 0 || math.Abs(float64(first[0]-first[1])) > float64(mixRate/50)) {
			t.Fatalf("span %d first pulse frames: %v", i, first)
		}
	}
	var initial bytes.Buffer
	if err := renderWAV(t.Context(), &initial, os.Open, spans, 0, 100, mixRate, 2, 0.4); err != nil {
		t.Fatal(err)
	}
	for _, b := range initial.Bytes()[44:] {
		if b != 0 {
			t.Fatal("gap was not silent")
		}
	}
}

func TestRF64HeaderAndShortOutput(t *testing.T) {
	frames := int64(math.MaxUint32)/4 + 1024
	b := wavHeader(frames, 48000, 2)
	if len(b) != 80 || string(b[:4]) != "RF64" || string(b[72:76]) != "data" || binary.LittleEndian.Uint64(b[28:36]) != uint64(frames*4) || binary.LittleEndian.Uint32(b[76:]) != math.MaxUint32 {
		t.Fatalf("bad RF64 header %x", b)
	}
	if err := renderWAV(context.Background(), shortWriter{}, os.Open, nil, 0, 10, 8000, 1, 1); err != io.ErrShortWrite {
		t.Fatalf("short writer error = %v", err)
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }
