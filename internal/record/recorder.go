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
}

type Recorder struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	Level          chan float64
	Done           chan error
	done           chan struct{} // closed when ffmpeg exits; safe for multiple waiters
	exitErr        error
	muted          bool
	sourceOutputID int
	PCMReader      io.ReadCloser

	stderrMu   sync.Mutex
	stderrTail []string
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
		"-i", inputDevice,
		"-af", "asetnsamples=n=480,astats=metadata=1:reset=1,ametadata=print:file=/dev/stderr",
		"-c:a", codec,
		"-ar", strconv.Itoa(opts.SampleRate),
		"-ac", strconv.Itoa(opts.Channels),
	}

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
		args = append(args, "-f", inputFmt, "-i", inputDevice)
	}

	// Build filter_complex: mix all inputs then apply VU meter filters.
	n := len(devices)
	var inputLabels string
	for i := 0; i < n; i++ {
		inputLabels += fmt.Sprintf("[%d:a]", i)
	}
	filterComplex := fmt.Sprintf(
		"%samix=inputs=%d:duration=longest,asetnsamples=n=480,astats=metadata=1:reset=1,ametadata=print:file=/dev/stderr[a]",
		inputLabels, n,
	)
	args = append(args, "-filter_complex", filterComplex)
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
// 16 kHz — suitable for live speech transcription.
func appendPCMPipeArgs(args []string, pipeFd int) []string {
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
		// ExtraFiles[0] becomes fd 3 in the child process.
		args = appendPCMPipeArgs(args, 3)
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

	r := &Recorder{
		cmd:            cmd,
		stdin:          stdin,
		Level:          make(chan float64, 10),
		Done:           make(chan error, 1),
		done:           make(chan struct{}),
		sourceOutputID: -1,
	}

	// Using a Writer (rather than StderrPipe + goroutine) lets cmd.Wait()
	// synchronize with the stderr drain automatically — guaranteeing the
	// tail buffer is fully populated by the time we read it on exit.
	cmd.Stderr = &stderrTap{r: r}

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

	go r.discoverSourceOutput()
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

// ToggleMute toggles per-stream mute on the ffmpeg PulseAudio source-output.
func (r *Recorder) ToggleMute() {
	r.muted = !r.muted
	if r.sourceOutputID >= 0 {
		muteSourceOutput(r.sourceOutputID, r.muted)
	}
}

// IsMuted returns whether the recorder is currently muted.
func (r *Recorder) IsMuted() bool {
	return r.muted
}

// discoverSourceOutput finds the PulseAudio source-output for this recorder's
// ffmpeg process. Called in a goroutine after Start.
func (r *Recorder) discoverSourceOutput() {
	if r.cmd.Process == nil {
		return
	}
	pid := r.cmd.Process.Pid
	for i := 0; i < 10; i++ {
		id, err := findSourceOutputByPID(pid)
		if err == nil {
			r.sourceOutputID = id
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (r *Recorder) Stop() {
	if r.muted && r.sourceOutputID >= 0 {
		muteSourceOutput(r.sourceOutputID, false)
		r.muted = false
	}
	r.stdin.Write([]byte("q"))
	r.stdin.Close()
}

// Wait blocks until ffmpeg has fully exited and the output file is finalized.
func (r *Recorder) Wait() error {
	<-r.done
	return r.exitErr
}

func EnsureOutputDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}
