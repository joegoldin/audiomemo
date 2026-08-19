package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/stream"
	"github.com/joegoldin/audiomemo/internal/transcribe"
)

// levelInterval is the coalescing window for mic readings. ffmpeg prints one
// RMS line per 480 samples, so 50 ms turns ~100 events/s into 20.
const levelInterval = 50 * time.Millisecond

// pumpLevels forwards ffmpeg's RMS readings as level events until the
// recorder closes the channel, which it does when ffmpeg exits.
func pumpLevels(em *stream.Emitter, levels <-chan float64, th *stream.LevelThrottle) {
	for db := range levels {
		if peak, ok := th.Push(db); ok {
			em.Level(stream.NormalizeLevel(peak), stream.ClampDB(peak))
		}
	}
}

// pumpText forwards the Streamer's two text channels. Partial replaces the
// previous partial; commit is final and appended. Stop() closes both, which
// is how this returns.
func pumpText(em *stream.Emitter, partial, committed <-chan string) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for text := range partial {
			em.Partial(text)
		}
	}()
	go func() {
		defer wg.Done()
		for text := range committed {
			em.Commit(text)
		}
	}()
	wg.Wait()
}

// pumpErrors forwards session failures from the realtime backend. They are
// never fatal to the recording: the Streamer either reconnects or gives up,
// and ffmpeg keeps writing the audio file either way.
func pumpErrors(em *stream.Emitter, errs <-chan error) {
	for err := range errs {
		em.Error(stream.ScopeStream, false, err)
	}
}

// endReason classifies why the stream is closing. A signal outranks a
// non-zero ffmpeg status, because tearing down the PCM pipe on stop routinely
// produces one and the user still got what they asked for.
func endReason(signalled bool, runErr error) string {
	switch {
	case signalled:
		return stream.ReasonSignal
	case runErr != nil:
		return stream.ReasonError
	default:
		return stream.ReasonStopped
	}
}

func endExitCode(signalled bool, runErr error) int {
	if signalled || runErr == nil {
		return 0
	}
	return 1
}

// runRecordStream is the --stream counterpart of runRecord's --no-tui branch.
// It receives an already-started streamer (or nil plus the reason it is nil)
// so the start event's mode is a fact rather than an intention.
func runRecordStream(
	cfg *config.Config,
	opts record.RecordOpts,
	rec *record.Recorder,
	streamer *transcribe.Streamer,
	streamErr error,
	batchTranscribe bool,
) error {
	em := stream.NewEmitter(os.Stdout)

	// The streamer failed to connect but the recording is fine, so the
	// consumer is told and the run continues without partials.
	if streamErr != nil {
		em.Error(stream.ScopeStream, false, streamErr)
	}

	mode := resolveStreamMode(streamer != nil, batchTranscribe)
	startEv := stream.StartEvent{
		Device:      opts.Device,
		DeviceLabel: opts.DeviceLabel,
		Devices:     opts.Devices,
		Path:        opts.OutputPath,
		Format:      opts.Format,
		SampleRate:  opts.SampleRate,
		Channels:    opts.Channels,
		Mode:        mode,
	}
	if streamer != nil {
		startEv.Backend = transcribe.RealtimeBackendName
	}
	em.Start(startEv)

	var pumps sync.WaitGroup
	pumps.Add(1)
	go func() { defer pumps.Done(); pumpLevels(em, rec.Level, stream.NewLevelThrottle(levelInterval)) }()
	if streamer != nil {
		pumps.Add(2)
		go func() { defer pumps.Done(); pumpText(em, streamer.Partial, streamer.Committed) }()
		go func() { defer pumps.Done(); pumpErrors(em, streamer.Err) }()
	}

	// --no-tui has no signal handler today: Ctrl+C kills the process and
	// ffmpeg finalises the file on its own. A consumer needs more than that,
	// so --stream stops ffmpeg the same graceful way the TUI's `q` does and
	// then closes the stream deliberately.
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	signalled := make(chan struct{})
	var signalOnce sync.Once
	go func() {
		<-sigCtx.Done()
		// Restore the default disposition first, so a second Ctrl+C kills the
		// process outright rather than waiting on a wedged ffmpeg.
		stopSignals()
		signalOnce.Do(func() { close(signalled) })
		rec.Stop()
	}()

	runErr := <-rec.Done
	wasSignalled := false
	select {
	case <-signalled:
		wasSignalled = true
	default:
	}

	if err := rec.Wait(); err != nil && !wasSignalled {
		// ffmpeg exits non-zero on a broken PCM pipe even when the audio file
		// is valid, so this is reported and not returned.
		em.Error(stream.ScopeRecord, false, err)
	}

	if streamer != nil {
		streamer.Stop()
	}
	pumps.Wait()

	if promoted, err := promoteLiveTranscript(opts.OutputPath); err != nil {
		em.Error(stream.ScopeRecord, false, fmt.Errorf("promoting live transcript: %w", err))
	} else if promoted != "" {
		_ = promoted
	}

	emitFinal(em, cfg, opts.OutputPath, streamer, batchTranscribe)

	em.End(stream.EndEvent{
		Reason:   endReason(wasSignalled, runErr),
		Path:     opts.OutputPath,
		ExitCode: endExitCode(wasSignalled, runErr),
	})
	return nil
}

// emitFinal is filled in by task 5, where the batch pass gets captured.
func emitFinal(em *stream.Emitter, cfg *config.Config, audioPath string, streamer *transcribe.Streamer, batchTranscribe bool) {
}
