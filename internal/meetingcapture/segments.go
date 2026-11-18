package meetingcapture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
)

const segmentDuration = 30 * time.Second
const maxTimelineMS = int64(365 * 24 * time.Hour / time.Millisecond)
const WAVHeaderSize = 44

var segmentPattern = regexp.MustCompile(`^tracks/([0-9]{3})-([0-9]{6})-([0-9]{6})-([0-9]{1,11})-([0-9]{4,6})-([1-8])\.wav$`)

func segmentName(name string) (id string, epoch, seq int, start int64, rate, channels int, ok bool) {
	a := segmentPattern.FindStringSubmatch(name)
	if a == nil {
		return
	}
	id = a[1]
	epoch, _ = strconv.Atoi(a[2])
	seq, _ = strconv.Atoi(a[3])
	start, _ = strconv.ParseInt(a[4], 10, 64)
	rate, _ = strconv.Atoi(a[5])
	channels, _ = strconv.Atoi(a[6])
	ok = start <= maxTimelineMS && validPCM(rate, channels)
	return
}

func validPCM(rate, channels int) bool {
	return rate >= 8000 && rate <= 192000 && channels >= 1 && channels <= 8
}

// Segment is a recoverable sample span. EndMS can differ slightly from the
// nominal sample duration because independently clocked devices drift.
type Segment struct {
	File                   string
	StartMS, EndMS, Frames int64
	Rate, Channels         int
	epoch, seq             int
}

func header(rate, channels int, size uint32) []byte {
	h := make([]byte, WAVHeaderSize)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+size)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(h[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], size)
	return h
}

func discoverSegments(tracks *os.Root, manifest *BundleManifest) (map[string][]Segment, error) {
	entries, err := fs.ReadDir(tracks.FS(), ".")
	if err != nil {
		return nil, err
	}
	desc := make(map[string][]Segment, len(manifest.Tracks))
	for _, t := range manifest.Tracks {
		desc[t.ID] = nil
	}
	damaged := make(map[string]bool)
	for _, entry := range entries {
		name := "tracks/" + entry.Name()
		id, epoch, seq, start, rate, ch, ok := segmentName(name)
		if !ok {
			continue
		}
		if _, ok := desc[id]; !ok {
			return nil, fmt.Errorf("unexpected source track %s", id)
		}
		f, err := openRegular(tracks, entry.Name())
		if err != nil {
			return nil, err
		}
		span, err := inspectSegment(f, Segment{File: name, StartMS: start, Rate: rate, Channels: ch, epoch: epoch, seq: seq})
		closeErr := f.Close()
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			damaged[id] = true
			continue
		}
		if err != nil || closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		if span.Frames > 0 {
			desc[id] = append(desc[id], span)
		} else {
			damaged[id] = true
		}
	}
	duration := manifest.DurationMS
	for i := range manifest.Tracks {
		t := &manifest.Tracks[i]
		spans := desc[t.ID]
		sort.Slice(spans, func(i, j int) bool { return spans[i].seq < spans[j].seq })
		for j := range spans {
			s := &spans[j]
			if j > 0 {
				prev := spans[j-1]
				if s.seq <= prev.seq || s.epoch < prev.epoch {
					return nil, fmt.Errorf("duplicate or unordered segment for track %s", t.ID)
				}
				if s.StartMS < prev.EndMS {
					s.StartMS = prev.EndMS
				}
			}
			nominal := (s.Frames*1000 + int64(s.Rate)/2) / int64(s.Rate)
			s.EndMS = s.StartMS + max(1, nominal)
			if j+1 < len(spans) && s.epoch == spans[j+1].epoch && s.seq+1 == spans[j+1].seq {
				next := spans[j+1].StartMS
				tolerance := max(int64(2), nominal/200) // reject >5000 ppm as an unreliable anchor
				if delta := next - s.EndMS; next > s.StartMS && delta >= -tolerance && delta <= tolerance {
					s.EndMS = next
				}
			}
			duration = max(duration, s.EndMS)
		}
		expected := len(t.Files)
		t.Files = nil
		for _, s := range spans {
			t.Files = append(t.Files, s.File)
		}
		if len(spans) > 0 {
			t.OffsetMS, t.Rate, t.Channels = spans[0].StartMS, spans[0].Rate, spans[0].Channels
		}
		if damaged[t.ID] || expected > len(spans) {
			t.Error = appendError(t.Error, "incomplete source segment")
		}
		if len(spans) == 0 {
			t.Error = appendError(t.Error, "no audio captured")
		}
	}
	if duration > maxTimelineMS {
		return nil, errors.New("meeting duration exceeds format limit")
	}
	manifest.DurationMS = duration
	for i := range manifest.Tracks {
		t := &manifest.Tracks[i]
		t.Gaps = nil
		var end int64
		for _, s := range desc[t.ID] {
			if s.StartMS > end {
				t.Gaps = append(t.Gaps, sessiontypes.Gap{StartMS: end, EndMS: s.StartMS})
			}
			end = s.EndMS
		}
		if end < duration {
			t.Gaps = append(t.Gaps, sessiontypes.Gap{StartMS: end, EndMS: duration})
		}
	}
	return desc, nil
}

func appendError(old, msg string) string {
	if old == "" {
		return msg
	}
	if strings.Contains(old, msg) {
		return old
	}
	return old + "; " + msg
}

func inspectSegment(f *os.File, s Segment) (Segment, error) {
	st, err := f.Stat()
	if err != nil {
		return s, err
	}
	var h [WAVHeaderSize]byte
	if _, err := io.ReadFull(f, h[:]); err != nil {
		return s, err
	}
	size := binary.LittleEndian.Uint32(h[40:])
	// A crashed writer's header still advertises the maximum segment capacity;
	// derive the actual recoverable length from complete frames on disk.
	if string(h[:]) != string(header(s.Rate, s.Channels, size)) || int64(size) > int64(s.Rate*s.Channels*2)*int64(segmentDuration/time.Second) {
		return s, fmt.Errorf("invalid source WAV: %s", s.File)
	}
	s.Frames = min(int64(size), st.Size()-WAVHeaderSize) / int64(s.Channels*2)
	return s, nil
}
