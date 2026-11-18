package record

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/joegoldin/audiomemo/internal/meetingcapture"
	"github.com/joegoldin/audiomemo/internal/sessiontypes"
)

// OpenMeetingSource uses one independent FFmpeg input per endpoint. Ordinary
// devices are normalized to 48 kHz stereo PCM, and the native macOS tap keeps
// its rate and channel count (converting float samples to signed 16-bit PCM).
func OpenMeetingSource(ctx context.Context, s sessiontypes.Source) (meetingcapture.Stream, error) {
	opts := RecordOpts{}
	rate, channels := 48000, 2
	var tap *systemAudioCapture
	if s.Device == SystemAudioDevice {
		var err error
		tap, err = startSystemAudio()
		if err != nil {
			return meetingcapture.Stream{}, err
		}
		rate, channels = tap.sampleRate, tap.channels
		opts.tapSampleRate, opts.tapChannels = rate, channels
	}
	input := ffmpegInputArgs(s.Device, opts)
	if tap == nil && InputFormat() == "pulse" {
		// The default Pulse fragment can be longer than the alignment budget.
		// Ask for 10 ms at the device format used by the Pulse demuxer.
		input = append([]string{"-fragment_size", "1920"}, input...)
	}
	stream, err := openMeetingFFmpeg(ctx, input, rate, channels, tap)
	if err != nil && tap != nil {
		err = errors.Join(err, tap.close())
	}
	return stream, err
}

func openMeetingFFmpeg(ctx context.Context, input []string, rate, channels int, tap *systemAudioCapture) (meetingcapture.Stream, error) {
	args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-probesize", "32", "-analyzeduration", "10000"}, input...)
	args = append(args, "-map", "0:a:0", "-ar", fmt.Sprint(rate), "-ac", fmt.Sprint(channels), "-c:a", "pcm_s16le", "-flush_packets", "1", "-f", "s16le", "pipe:1")
	life, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(life, "ffmpeg", args...)
	if tap != nil {
		cmd.ExtraFiles = []*os.File{tap.reader}
	}
	r, w, err := os.Pipe()
	if err != nil {
		cancel()
		return meetingcapture.Stream{}, err
	}
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		cancel()
		w.Close()
		r.Close()
		return meetingcapture.Stream{}, fmt.Errorf("start meeting FFmpeg: %w", err)
	}
	w.Close()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	var once sync.Once
	var closeErr error
	pcm := &meetingPCM{File: r, close: func() error {
		once.Do(func() {
			cancel()
			closeErr = r.Close()
			if tap != nil {
				closeErr = errors.Join(closeErr, tap.close())
			}
			<-done
		})
		return closeErr
	}}
	return meetingcapture.Stream{PCM: pcm, Rate: rate, Channels: channels}, nil
}

type meetingPCM struct {
	*os.File
	close func() error
}

func (p *meetingPCM) Close() error { return p.close() }
