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
	"time"
)

type RecordOpts struct {
	Device     string
	Format     string
	SampleRate int
	Channels   int
	OutputPath string
}

type Recorder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr io.ReadCloser
	Level  chan float64
	Done   chan error
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
		"-af", "astats=metadata=1:reset=1,ametadata=print:file=/dev/stderr",
		"-c:a", codec,
		"-ar", strconv.Itoa(opts.SampleRate),
		"-ac", strconv.Itoa(opts.Channels),
	}

	if codec == "libopus" {
		args = append(args, "-b:a", "64k")
	}

	args = append(args, "-y", opts.OutputPath)
	return args
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
	args := BuildFFmpegArgs(opts)
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
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	go r.parseStderr()
	go func() {
		r.Done <- cmd.Wait()
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

func (r *Recorder) Pause() {
	r.stdin.Write([]byte("c"))
}

func (r *Recorder) Stop() {
	r.stdin.Write([]byte("q"))
}

func EnsureOutputDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}
