package record

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type RecordOpts struct {
	Device      string
	Devices     []string
	DeviceLabel string
	Format      string
	SampleRate  int
	Channels    int
	OutputPath  string
}

type Recorder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr io.ReadCloser
	Level  chan float64
	Done   chan error
	done   chan struct{} // closed when ffmpeg exits; safe for multiple waiters
	exitErr error
	paused  bool
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

var rmsPattern = regexp.MustCompile(`lavfi\.astats\.Overall\.RMS_level=(-?[\d.]+|inf|-inf)`)

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
	cmd := exec.Command("ffmpeg", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	r := &Recorder{
		cmd:    cmd,
		stdin:  stdin,
		stderr: stderr,
		Level:  make(chan float64, 10),
		Done:   make(chan error, 1),
		done:   make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	go r.parseStderr()
	go func() {
		r.exitErr = cmd.Wait()
		r.Done <- r.exitErr
		close(r.done)
	}()

	return r, nil
}

func (r *Recorder) parseStderr() {
	defer close(r.Level)
	scanner := bufio.NewScanner(r.stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if m := rmsPattern.FindStringSubmatch(line); len(m) > 1 {
			if val, err := strconv.ParseFloat(m[1], 64); err == nil {
				select {
				case r.Level <- val:
				default:
				}
			}
		}
	}
}

// Pause toggles pause/resume using SIGSTOP/SIGCONT so ffmpeg's stdin
// command parser is never put into an unexpected state.
func (r *Recorder) Pause() {
	if r.cmd.Process == nil {
		return
	}
	if r.paused {
		r.cmd.Process.Signal(syscall.SIGCONT)
		r.paused = false
	} else {
		r.cmd.Process.Signal(syscall.SIGSTOP)
		r.paused = true
	}
}

func (r *Recorder) Stop() {
	// Resume first if paused, otherwise ffmpeg can't process the quit.
	if r.paused && r.cmd.Process != nil {
		r.cmd.Process.Signal(syscall.SIGCONT)
		r.paused = false
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
