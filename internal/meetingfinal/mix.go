package meetingfinal

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"

	"github.com/joegoldin/audiomemo/internal/meetingcapture"
)

const mixRate = 48000
const recognitionRate = 16000

type openAudio func(string) (*os.File, error)

type trackReader struct {
	open       openAudio
	spans      []meetingcapture.Segment
	file       *os.File
	buf        *bufio.Reader
	frame      [16]byte
	index      int64
	prev, next [2]float64
}

func (r *trackReader) close() {
	if r.file != nil {
		r.file.Close()
		r.file = nil
	}
}

func (r *trackReader) read(s meetingcapture.Segment) ([2]float64, error) {
	b := r.frame[:s.Channels*2]
	_, err := io.ReadFull(r.buf, b)
	if err != nil {
		return [2]float64{}, err
	}
	var pair [2]float64
	var count [2]int
	for i := 0; i < s.Channels; i++ {
		value := float64(int16(binary.LittleEndian.Uint16(b[i*2:])))
		pair[i%2] += value
		count[i%2]++
	}
	pair[0] /= float64(count[0])
	if count[1] == 0 {
		pair[1] = pair[0]
	} else {
		pair[1] /= float64(count[1])
	}
	return pair, nil
}

func audioFrame(ms int64, rate int) int64 { return (ms*int64(rate) + 500) / 1000 }

func (r *trackReader) at(frame int64, rate int) ([2]float64, error) {
	for len(r.spans) > 0 && frame >= audioFrame(r.spans[0].EndMS, rate) {
		r.close()
		r.spans = r.spans[1:]
	}
	if len(r.spans) == 0 {
		return [2]float64{}, nil
	}
	s := r.spans[0]
	first := audioFrame(s.StartMS, rate)
	if frame < first {
		return [2]float64{}, nil
	}
	length := audioFrame(s.EndMS, rate) - first
	product := (frame - first) * s.Frames
	index := product / length
	fraction := float64(product%length) / float64(length)
	if r.file == nil {
		f, err := r.open(s.File)
		if err != nil {
			return [2]float64{}, err
		}
		r.file = f
		if _, err := f.Seek(meetingcapture.WAVHeaderSize+index*int64(s.Channels*2), io.SeekStart); err != nil {
			return [2]float64{}, err
		}
		if r.buf == nil {
			r.buf = bufio.NewReaderSize(f, 32768)
		} else {
			r.buf.Reset(f)
		}
		r.next, err = r.read(s)
		if err != nil {
			return [2]float64{}, err
		}
		r.index = index - 1
	}
	for r.index < index {
		r.prev = r.next
		r.index++
		if r.index+1 >= s.Frames {
			break
		}
		var err error
		r.next, err = r.read(s)
		if err != nil {
			return [2]float64{}, err
		}
	}
	return [2]float64{r.prev[0] + (r.next[0]-r.prev[0])*fraction, r.prev[1] + (r.next[1]-r.prev[1])*fraction}, nil
}

// renderWAV synthesizes aligned samples on a fixed timeline without loading any
// track in memory. Gaps are zero; the fixed per-track gain leaves 1.9 dB headroom
// even if all inputs peak at once, and doesn't pump when a source disconnects.
func renderWAV(ctx context.Context, out io.Writer, open openAudio, spans [][]meetingcapture.Segment, from, to int64, rate, channels int, gain float64) error {
	first, last := audioFrame(from, rate), audioFrame(to, rate)
	if err := writeBlock(out, wavHeader(last-first, rate, channels)); err != nil {
		return err
	}
	readers := make([]trackReader, len(spans))
	for i, track := range spans {
		readers[i] = trackReader{open: open, spans: track}
		defer readers[i].close()
	}
	var block [16384]byte
	stride := channels * 2
	for first < last {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := int(min(last-first, int64(len(block)/stride)))
		for i := range size {
			var sum [2]float64
			for j := range readers {
				pair, err := readers[j].at(first+int64(i), rate)
				if err != nil {
					return err
				}
				sum[0] += pair[0]
				sum[1] += pair[1]
			}
			if channels == 1 {
				sum[0] = (sum[0] + sum[1]) / 2
			}
			for c := range channels {
				sample := int16(math.Round(max(-32768, min(32767, sum[c]*gain))))
				binary.LittleEndian.PutUint16(block[i*stride+c*2:], uint16(sample))
			}
		}
		if err := writeBlock(out, block[:size*stride]); err != nil {
			return err
		}
		first += int64(size)
	}
	return nil
}

func wavHeader(frames int64, rate, channels int) []byte {
	size := uint64(frames) * uint64(channels*2)
	var h []byte
	if size+36 > math.MaxUint32 {
		h = make([]byte, 80)
		copy(h, "RF64\xff\xff\xff\xffWAVEds64")
		binary.LittleEndian.PutUint32(h[16:], 28)
		binary.LittleEndian.PutUint64(h[20:], size+72)
		binary.LittleEndian.PutUint64(h[28:], size)
		binary.LittleEndian.PutUint64(h[36:], uint64(frames))
	} else {
		h = make([]byte, 44)
		copy(h, "RIFF")
		binary.LittleEndian.PutUint32(h[4:], uint32(size+36))
		copy(h[8:], "WAVE")
	}
	p := h[len(h)-32:]
	copy(p, "fmt ")
	binary.LittleEndian.PutUint32(p[4:], 16)
	binary.LittleEndian.PutUint16(p[8:], 1)
	binary.LittleEndian.PutUint16(p[10:], uint16(channels))
	binary.LittleEndian.PutUint32(p[12:], uint32(rate))
	binary.LittleEndian.PutUint32(p[16:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(p[20:], uint16(channels*2))
	binary.LittleEndian.PutUint16(p[22:], 16)
	copy(p[24:], "data")
	if len(h) == 80 {
		binary.LittleEndian.PutUint32(p[28:], math.MaxUint32)
	} else {
		binary.LittleEndian.PutUint32(p[28:], uint32(size))
	}
	// The chunk starts immediately after WAVE (classic) or ds64 (RF64).
	return h
}

func writeBlock(out io.Writer, b []byte) error {
	n, err := out.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}
