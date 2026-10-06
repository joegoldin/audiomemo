package transcribe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/joegoldin/audiomemo/internal/config"
)

func NewDispatcher(cfg *config.Config, backendOverride string) (Transcriber, error) {
	backend := backendOverride
	if backend == "" {
		backend = cfg.Transcribe.DefaultBackend
	}

	if backend != "" && backend != "auto" {
		return newBackend(cfg, backend)
	}
	local := NewNemo(cfg.Transcribe.Nemo)
	var fallback Transcriber
	if cfg.Transcribe.ElevenLabs.APIKey != "" {
		fallback = NewElevenLabs(cfg.Transcribe.ElevenLabs.APIKey, cfg.Transcribe.ElevenLabs.Model, cfg.Transcribe.ElevenLabs.StoreInCloud)
	}
	return &localFirst{local: local, fallback: fallback}, nil
}

// Explicit backend choices never silently send local recordings to a service.
// Only auto mode authorizes the configured ElevenLabs fallback.
type localFirst struct {
	local    Transcriber
	fallback Transcriber
}

func (f *localFirst) Name() string { return "auto" }
func (f *localFirst) Transcribe(ctx context.Context, path string, opts TranscribeOpts) (*Result, error) {
	if err := validateOpts(f.Name(), opts, true, false, false, false, false); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	result, err := f.local.Transcribe(ctx, path, opts)
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if f.fallback == nil {
		return nil, fmt.Errorf("local transcription failed (no ElevenLabs key configured): %w", err)
	}
	fmt.Fprintf(os.Stderr, "Local transcription failed: %v\nFalling back to ElevenLabs; the recording will be uploaded.\n", err)
	// A local model name/path cannot be used as an ElevenLabs model ID.
	opts.Model = ""
	result, fallbackErr := f.fallback.Transcribe(ctx, path, opts)
	if fallbackErr != nil {
		return nil, errors.Join(err, fmt.Errorf("ElevenLabs fallback: %w", fallbackErr))
	}
	return result, nil
}

func newBackend(cfg *config.Config, name string) (Transcriber, error) {
	hfToken := cfg.Transcribe.Whisper.HFToken
	switch name {
	case "nemo", "local":
		return NewNemo(cfg.Transcribe.Nemo), nil
	case "whisper":
		// Auto-detect best whisper variant
		if w, found := DetectWhisper(cfg.Transcribe.Whisper.Model); found {
			w.hfToken = hfToken
			return w, nil
		}
		// Fall back to configured binary or "whisper"
		binary := cfg.Transcribe.Whisper.Binary
		if binary == "" {
			binary = "whisper"
		}
		w := NewWhisper(binary, cfg.Transcribe.Whisper.Model)
		w.hfToken = hfToken
		return w, nil
	case "whisper-cpp":
		w := NewWhisper("whisper-cli", cfg.Transcribe.Whisper.Model)
		w.hfToken = hfToken
		return w, nil
	case "whisperx":
		w := NewWhisper("whisperx", cfg.Transcribe.Whisper.Model)
		w.hfToken = hfToken
		return w, nil
	case "ffmpeg-whisper":
		binary := "ffmpeg"
		if b, err := exec.LookPath("ffmpeg"); err == nil {
			binary = b
		}
		return &Whisper{binary: binary, variant: variantFFmpegWhisper, defaultModel: cfg.Transcribe.Whisper.Model, hfToken: hfToken}, nil
	case "elevenlabs":
		if cfg.Transcribe.ElevenLabs.APIKey == "" {
			return nil, fmt.Errorf("elevenlabs API key not configured")
		}
		return NewElevenLabs(cfg.Transcribe.ElevenLabs.APIKey, cfg.Transcribe.ElevenLabs.Model, cfg.Transcribe.ElevenLabs.StoreInCloud), nil
	case "deepgram":
		if cfg.Transcribe.Deepgram.APIKey == "" {
			return nil, fmt.Errorf("deepgram API key not configured")
		}
		return NewDeepgram(cfg.Transcribe.Deepgram.APIKey, cfg.Transcribe.Deepgram.Model), nil
	case "openai":
		if cfg.Transcribe.OpenAI.APIKey == "" {
			return nil, fmt.Errorf("openai API key not configured")
		}
		return NewOpenAI(cfg.Transcribe.OpenAI.APIKey, cfg.Transcribe.OpenAI.Model), nil
	case "mistral":
		if cfg.Transcribe.Mistral.APIKey == "" {
			return nil, fmt.Errorf("mistral API key not configured")
		}
		return NewMistral(cfg.Transcribe.Mistral.APIKey, cfg.Transcribe.Mistral.Model), nil
	default:
		return nil, fmt.Errorf("unknown backend: %s (available: auto, nemo, elevenlabs, whisper, whisper-cpp, whisperx, ffmpeg-whisper, deepgram, openai, mistral)", name)
	}
}
