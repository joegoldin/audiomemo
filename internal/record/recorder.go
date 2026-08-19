package record

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxStderrTailLines bounds how many non-RMS stderr lines we retain so the
// recorder can surface them when ffmpeg exits with an error.
const maxStderrTailLines = 30

type RecordOpts struct {
	Device      string
	Devices     []string
	DeviceLabel string
	Format      string
	SampleRate  int
	Channels    int
	OutputPath  string
	LivePCM     bool

	// MaxDuration caps the capture length. It becomes ffmpeg's own -t, so
	// ffmpeg finalises the file and exits on its own; every run loop already
	// watches Recorder.Done and so needs no further handling.
	MaxDuration time.Duration
	// MaxSilence stops the recording once the room has been quiet for this
	// long. Zero leaves silence detection off.
	MaxSilence time.Duration
	// SilenceThreshold is the dBFS level at or below which a reading counts as
	// silence. Zero means DefaultSilenceThreshold.
	SilenceThreshold float64
}

type Recorder struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	Level     chan float64
	Done      chan error
	done      chan struct{} // closed when ffmpeg exits; safe for multiple waiters
	exitErr   error
	PCMReader io.ReadCloser

	// muteMu guards muted and sourceOutputIDs: the discovery goroutine writes
	// the IDs while the TUI goroutine reads them from ToggleMute/Stop.
	muteMu          sync.Mutex
	muted           bool
	sourceOutputIDs []int
	// expectedSources is how many source-outputs ffmpeg should open — one per
	// input device. Discovery keeps polling until it has them all.
	expectedSources int

	stderrMu   sync.Mutex
	stderrTail []string

	// stopFn ends the recording when silence detection trips. Start points it
	// at Stop; a Recorder built without a live ffmpeg has no stdin to write
	// "q" to, so tests point it at a spy instead.
	stopFn func()

	silenceMu      sync.Mutex
	silenceStopped bool
}

func InputFormat() string {
	if runtime.GOOS == "darwin" {
		return "avfoundation"
	}
	return "pulse"
}

func CodecForFormat(format string) string {
	switch format {
	case "wav":
		return "pcm_s16le"
	case "flac":
		return "flac"
	case "mp3":
		return "libmp3lame"
	default:
		return "libopus"
	}
}

// ffmpegDurationArgs renders MaxDuration as ffmpeg's -t, in seconds. It
// returns nil when unset so callers can append it unconditionally.
func ffmpegDurationArgs(d time.Duration) []string {
	if d <= 0 {
		return nil
	}
	return []string{"-t", strconv.FormatFloat(d.Seconds(), 'f', -1, 64)}
}

func BuildFFmpegArgs(opts RecordOpts) []string {
	inputFmt := InputFormat()
	device := opts.Device
	if device == "" {
		device = "default"
	}

	// On macOS avfoundation, input device is ":index" for audio-only
	inputDevice := device
	if inputFmt == "avfoundation" && !strings.HasPrefix(device, ":") {
		inputDevice = ":" + device
	}

	codec := CodecForFormat(opts.Format)

	args := []string{
		"-f", inputFmt,
	}
	args = append(args, ffmpegDurationArgs(opts.MaxDuration)...)
	args = append(args,
		"-i", inputDevice,
		"-af", "asetnsamples=n=480,astats=metadata=1:reset=1,ametadata=print:file=/dev/stderr",
		"-c:a", codec,
		"-ar", strconv.Itoa(opts.SampleRate),
		"-ac", strconv.Itoa(opts.Channels),
	)

	if codec == "libopus" {
		args = append(args, "-b:a", "64k")
	}

	// Reset output timestamps to start from 0; PulseAudio may provide
	// timestamps based on stream start time, causing large PTS offsets
	// that break downstream tools expecting timestamps starting at 0.
	args = append(args, "-output_ts_offset", "0")

	args = append(args, "-y", opts.OutputPath)
	return args
}

// BuildFFmpegArgsMulti builds ffmpeg args for recording from multiple input
// devices simultaneously, mixing them into a single output via amix. For a
// single device it delegates to BuildFFmpegArgs. An empty device list returns
// an error.
func BuildFFmpegArgsMulti(opts RecordOpts) ([]string, error) {
	devices := opts.Devices
	if len(devices) == 0 {
		return nil, fmt.Errorf("BuildFFmpegArgsMulti: no devices specified")
	}
	if len(devices) == 1 {
		opts.Device = devices[0]
		return BuildFFmpegArgs(opts), nil
	}

	inputFmt := InputFormat()
	codec := CodecForFormat(opts.Format)

	var args []string

	// Add each input device.
	for _, dev := range devices {
		inputDevice := dev
		if inputFmt == "avfoundation" && !strings.HasPrefix(dev, ":") {
			inputDevice = ":" + dev
		}
		args = append(args, "-f", inputFmt)
		args = append(args, ffmpegDurationArgs(opts.MaxDuration)...)
		args = append(args, "-i", inputDevice)
	}

	// Build filter_complex: mix all inputs then apply VU meter filters.
	n := len(devices)
	var inputLabels string
	for i := 0; i < n; i++ {
		inputLabels += fmt.Sprintf("[%d:a]", i)
	}
	// When LivePCM is on, fork the mixed audio with asplit so the PCM pipe
	// output gets the mix too — not just input 0 (the first mic) which is
	// what ffmpeg auto-selects for an output without -map.
	filterGraph := fmt.Sprintf(
		"%samix=inputs=%d:duration=longest,asetnsamples=n=480,astats=metadata=1:reset=1,ametadata=print:file=/dev/stderr",
		inputLabels, n,
	)
	if opts.LivePCM {
		filterGraph += ",asplit=2[a][b]"
	} else {
		filterGraph += "[a]"
	}
	args = append(args, "-filter_complex", filterGraph)
	args = append(args, "-map", "[a]")

	args = append(args,
		"-c:a", codec,
		"-ar", strconv.Itoa(opts.SampleRate),
		"-ac", strconv.Itoa(opts.Channels),
	)

	if codec == "libopus" {
		args = append(args, "-b:a", "64k")
	}

	args = append(args, "-output_ts_offset", "0")
	args = append(args, "-y", opts.OutputPath)
	return args, nil
}

func GenerateFilename(format, label string) string {
	ts := time.Now().Format("2006-01-02T15-04-05")
	if label != "" {
		return fmt.Sprintf("%s-%s.%s", label, ts, format)
	}
	return fmt.Sprintf("recording-%s.%s", ts, format)
}

// GenerateClipFilename generates a filename for a clip with a sequence number.
// Format: {label}-{NNN}-{timestamp}.{format}
func GenerateClipFilename(format, label string, clipNumber int) string {
	ts := time.Now().Format("2006-01-02T15-04-05")
	return fmt.Sprintf("%s-%03d-%s.%s", label, clipNumber, ts, format)
}

var rmsPattern = regexp.MustCompile(`lavfi\.astats\.Overall\.RMS_level=(-?[\d.]+|inf|-inf)`)

// appendPCMPipeArgs appends ffmpeg output args that write a raw PCM stream to
// the given file descriptor. The stream is signed 16-bit little-endian, mono,
// 16 kHz — suitable for live speech transcription. If mapLabel is non-empty
// (e.g. "[b]" from a filter_complex asplit), it is mapped to this output so
// the pipe receives the mixed audio rather than the default first input.
func appendPCMPipeArgs(args []string, pipeFd int, mapLabel string) []string {
	if mapLabel != "" {
		args = append(args, "-map", mapLabel)
	}
	return append(args, "-f", "s16le", "-ar", "16000", "-ac", "1", fmt.Sprintf("pipe:%d", pipeFd))
}

func Start(opts RecordOpts) (*Recorder, error) {
	var args []string
	if len(opts.Devices) > 1 {
		var err error
		args, err = BuildFFmpegArgsMulti(opts)
		if err != nil {
			return nil, err
		}
	} else {
		// Single device: prefer Devices[0] if set, fall back to Device field.
		if len(opts.Devices) == 1 {
			opts.Device = opts.Devices[0]
		}
		args = BuildFFmpegArgs(opts)
	}
	var pcmReadEnd *os.File
	var pcmWriteEnd *os.File
	if opts.LivePCM {
		var err error
		pcmReadEnd, pcmWriteEnd, err = os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create PCM pipe: %w", err)
		}
		// ExtraFiles[0] becomes fd 3 in the child process. With multiple devices
		// the filter_complex exposes the mix as label [b] via asplit; pass that
		// so the pipe receives mixed audio rather than just input 0.
		mapLabel := ""
		if len(opts.Devices) > 1 {
			mapLabel = "[b]"
		}
		args = appendPCMPipeArgs(args, 3, mapLabel)
	}

	cmd := exec.Command("ffmpeg", args...)

	if opts.LivePCM {
		cmd.ExtraFiles = []*os.File{pcmWriteEnd}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		if pcmReadEnd != nil {
			pcmReadEnd.Close()
		}
		if pcmWriteEnd != nil {
			pcmWriteEnd.Close()
		}
		return nil, err
	}

	expectedSources := len(opts.Devices)
	if expectedSources < 1 {
		expectedSources = 1
	}
	r := &Recorder{
		cmd:             cmd,
		stdin:           stdin,
		Level:           make(chan float64, 10),
		Done:            make(chan error, 1),
		done:            make(chan struct{}),
		expectedSources: expectedSources,
	}

	r.stopFn = r.Stop

	threshold := opts.SilenceThreshold
	if threshold == 0 {
		threshold = DefaultSilenceThreshold
	}

	// Using a Writer (rather than StderrPipe + goroutine) lets cmd.Wait()
	// synchronize with the stderr drain automatically — guaranteeing the
	// tail buffer is fully populated by the time we read it on exit.
	//
	// The tap is also where silence detection lives: it sees every RMS
	// reading ffmpeg prints, whereas Recorder.Level drops readings when its
	// consumer falls behind.
	cmd.Stderr = &stderrTap{
		r:       r,
		silence: NewSilenceWatcher(threshold, opts.MaxSilence),
		now:     time.Now,
	}

	if err := cmd.Start(); err != nil {
		if pcmReadEnd != nil {
			pcmReadEnd.Close()
		}
		if pcmWriteEnd != nil {
			pcmWriteEnd.Close()
		}
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	if opts.LivePCM {
		// Close the write end in the parent; ffmpeg inherited it.
		pcmWriteEnd.Close()
		r.PCMReader = pcmReadEnd
	}

	go r.discoverSourceOutputs()
	go func() {
		exitErr := cmd.Wait()
		close(r.Level)
		if exitErr != nil {
			if tail := r.StderrTail(); tail != "" {
				exitErr = fmt.Errorf("%w\nffmpeg stderr:\n%s", exitErr, tail)
			}
		}
		r.exitErr = exitErr
		r.Done <- exitErr
		close(r.done)
	}()

	return r, nil
}

// stderrTap is an io.Writer attached to ffmpeg's stderr. It splits incoming
// bytes on newlines and dispatches each line: RMS-level lines feed the VU
// meter via Recorder.Level; everything else accumulates in a bounded tail
// buffer so failure context can be returned when ffmpeg exits non-zero.
type stderrTap struct {
	r       *Recorder
	pending []byte // buffer for partial trailing line across Write calls
	silence *SilenceWatcher
	now     func() time.Time
}

func (s *stderrTap) Write(p []byte) (int, error) {
	s.pending = append(s.pending, p...)
	for {
		idx := bytes.IndexByte(s.pending, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(string(s.pending[:idx]), "\r")
		s.pending = s.pending[idx+1:]
		s.handleLine(line)
	}
	return len(p), nil
}

func (s *stderrTap) handleLine(line string) {
	if m := rmsPattern.FindStringSubmatch(line); len(m) > 1 {
		if val, err := strconv.ParseFloat(m[1], 64); err == nil {
			// Guarded rather than relying on Push's nil-safety: with
			// silence detection off there is no clock to read, and no reason
			// to read one a hundred times a second.
			if s.silence != nil && s.silence.Push(val, s.now()) {
				s.r.stopForSilence()
			}
			select {
			case s.r.Level <- val:
			default:
			}
		}
		return
	}
	s.r.appendStderrLine(line)
}

func (r *Recorder) appendStderrLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	r.stderrMu.Lock()
	defer r.stderrMu.Unlock()
	r.stderrTail = append(r.stderrTail, line)
	if len(r.stderrTail) > maxStderrTailLines {
		r.stderrTail = r.stderrTail[len(r.stderrTail)-maxStderrTailLines:]
	}
}

// StderrTail returns the captured non-RMS stderr lines joined by newlines.
// Useful for diagnosing why ffmpeg exited.
func (r *Recorder) StderrTail() string {
	r.stderrMu.Lock()
	defer r.stderrMu.Unlock()
	return strings.Join(r.stderrTail, "\n")
}

// ToggleMute toggles mute across every PulseAudio source-output this
// recording owns. A device group records N inputs through one ffmpeg process,
// so muting only the first would leave the rest still capturing.
func (r *Recorder) ToggleMute() {
	r.muteMu.Lock()
	r.muted = !r.muted
	muted := r.muted
	ids := append([]int(nil), r.sourceOutputIDs...)
	r.muteMu.Unlock()

	for _, id := range ids {
		muteSourceOutputFn(id, muted)
	}
}

// IsMuted returns whether the recorder is currently muted.
func (r *Recorder) IsMuted() bool {
	r.muteMu.Lock()
	defer r.muteMu.Unlock()
	return r.muted
}

// setSourceOutputIDs records the discovered source-outputs, applying the
// current mute state to any that appeared after the user already hit mute.
func (r *Recorder) setSourceOutputIDs(ids []int) {
	r.muteMu.Lock()
	known := make(map[int]struct{}, len(r.sourceOutputIDs))
	for _, id := range r.sourceOutputIDs {
		known[id] = struct{}{}
	}
	var fresh []int
	for _, id := range ids {
		if _, seen := known[id]; !seen {
			fresh = append(fresh, id)
		}
	}
	r.sourceOutputIDs = append([]int(nil), ids...)
	muted := r.muted
	r.muteMu.Unlock()

	if muted {
		for _, id := range fresh {
			muteSourceOutputFn(id, true)
		}
	}
}

// discoverSourceOutputs finds the PulseAudio source-outputs for this
// recorder's ffmpeg process. Called in a goroutine after Start. Inputs connect
// one at a time, so it keeps polling until every expected source-output has
// appeared, then settles for whatever it found.
func (r *Recorder) discoverSourceOutputs() {
	if r.cmd.Process == nil {
		return
	}
	pid := r.cmd.Process.Pid
	want := r.expectedSources
	if want < 1 {
		want = 1
	}
	for i := 0; i < 20; i++ {
		if ids, err := findSourceOutputsByPID(pid); err == nil {
			r.setSourceOutputIDs(ids)
			if len(ids) >= want {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (r *Recorder) Stop() {
	r.muteMu.Lock()
	wasMuted := r.muted
	ids := append([]int(nil), r.sourceOutputIDs...)
	r.muted = false
	r.muteMu.Unlock()

	if wasMuted {
		for _, id := range ids {
			muteSourceOutputFn(id, false)
		}
	}
	r.stdin.Write([]byte("q"))
	r.stdin.Close()
}

// Wait blocks until ffmpeg has fully exited and the output file is finalized.
// stopForSilence ends the recording because --max-silence elapsed. The
// SilenceWatcher fires once, so this runs once. It stops in the background
// because the caller is the goroutine draining ffmpeg's stderr, which must
// not block on the stdin write that Stop performs.
func (r *Recorder) stopForSilence() {
	r.silenceMu.Lock()
	r.silenceStopped = true
	r.silenceMu.Unlock()
	go r.stopFn()
}

// StoppedForSilence reports whether the recording ended because it went quiet
// for longer than --max-silence, so callers can say so rather than leaving an
// unexplained early exit.
func (r *Recorder) StoppedForSilence() bool {
	r.silenceMu.Lock()
	defer r.silenceMu.Unlock()
	return r.silenceStopped
}

func (r *Recorder) Wait() error {
	<-r.done
	return r.exitErr
}

func EnsureOutputDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}
