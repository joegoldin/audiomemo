package transcribe

import (
	"fmt"
	"os/exec"

	"github.com/joegilkes/audiotools/internal/config"
)

func NewDispatcher(cfg *config.Config, backendOverride string) (Transcriber, error) {
	backend := backendOverride
	if backend == "" {
		backend = cfg.Transcribe.DefaultBackend
	}

	if backend != "" {
		return newBackend(cfg, backend)
	}

	// Auto-detect: scan for configured API keys
	if cfg.Transcribe.Deepgram.APIKey != "" {
		return NewDeepgram(cfg.Transcribe.Deepgram.APIKey, cfg.Transcribe.Deepgram.Model), nil
	}
	if cfg.Transcribe.OpenAI.APIKey != "" {
		return NewOpenAI(cfg.Transcribe.OpenAI.APIKey, cfg.Transcribe.OpenAI.Model), nil
	}
	if cfg.Transcribe.Mistral.APIKey != "" {
		return NewMistral(cfg.Transcribe.Mistral.APIKey, cfg.Transcribe.Mistral.Model), nil
	}

	// Check for local whisper (whisper-cli, whisper, whisperx)
	if w, found := DetectWhisper(cfg.Transcribe.Whisper.Model); found {
		return w, nil
	}

	return nil, fmt.Errorf("no transcription backend available. Set an API key (DEEPGRAM_API_KEY, OPENAI_API_KEY, MISTRAL_API_KEY) or install whisper locally")
}

func newBackend(cfg *config.Config, name string) (Transcriber, error) {
	switch name {
	case "whisper":
		// Auto-detect best whisper variant
		if w, found := DetectWhisper(cfg.Transcribe.Whisper.Model); found {
			return w, nil
		}
		// Fall back to configured binary or "whisper"
		binary := cfg.Transcribe.Whisper.Binary
		if binary == "" {
			binary = "whisper"
		}
		return NewWhisper(binary, cfg.Transcribe.Whisper.Model), nil
	case "whisper-cpp":
		return NewWhisper("whisper-cli", cfg.Transcribe.Whisper.Model), nil
	case "whisperx":
		return NewWhisper("whisperx", cfg.Transcribe.Whisper.Model), nil
	case "ffmpeg-whisper":
		binary := "ffmpeg"
		if b, err := exec.LookPath("ffmpeg"); err == nil {
			binary = b
		}
		return &Whisper{binary: binary, variant: variantFFmpegWhisper, defaultModel: cfg.Transcribe.Whisper.Model}, nil
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
		return nil, fmt.Errorf("unknown backend: %s (available: whisper, whisper-cpp, whisperx, ffmpeg-whisper, deepgram, openai, mistral)", name)
	}
}
