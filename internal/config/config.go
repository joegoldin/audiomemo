package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

type Config struct {
	Record     RecordConfig     `toml:"record"`
	Transcribe TranscribeConfig `toml:"transcribe"`
}

type RecordConfig struct {
	Format     string `toml:"format"`
	SampleRate int    `toml:"sample_rate"`
	Channels   int    `toml:"channels"`
	OutputDir  string `toml:"output_dir"`
	Device     string `toml:"device"`
}

type TranscribeConfig struct {
	DefaultBackend string         `toml:"default_backend"`
	Language       string         `toml:"language"`
	OutputFormat   string         `toml:"output_format"`
	Whisper        WhisperConfig  `toml:"whisper"`
	Deepgram       DeepgramConfig `toml:"deepgram"`
	OpenAI         OpenAIConfig   `toml:"openai"`
	Mistral        MistralConfig  `toml:"mistral"`
}

type WhisperConfig struct {
	Model  string `toml:"model"`
	Binary string `toml:"binary"`
}

type DeepgramConfig struct {
	APIKey string `toml:"api_key"`
	Model  string `toml:"model"`
}

type OpenAIConfig struct {
	APIKey string `toml:"api_key"`
	Model  string `toml:"model"`
}

type MistralConfig struct {
	APIKey string `toml:"api_key"`
	Model  string `toml:"model"`
}

func Default() *Config {
	return &Config{
		Record: RecordConfig{
			Format:     "ogg",
			SampleRate: 48000,
			Channels:   1,
			OutputDir:  "~/Recordings",
		},
		Transcribe: TranscribeConfig{
			OutputFormat: "text",
			Whisper:      WhisperConfig{Model: "base", Binary: "whisper"},
			Deepgram:     DeepgramConfig{Model: "nova-3"},
			OpenAI:       OpenAIConfig{Model: "gpt-4o-transcribe"},
			Mistral:      MistralConfig{Model: "voxtral-mini-latest"},
		},
	}
}

func Load() (*Config, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Default(), nil
		}
		configDir = filepath.Join(home, ".config")
	}
	return LoadFrom(filepath.Join(configDir, "audiotools", "config.toml"))
}

func LoadFrom(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return nil, err
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) ApplyEnv() {
	if v := os.Getenv("DEEPGRAM_API_KEY"); v != "" {
		c.Transcribe.Deepgram.APIKey = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		c.Transcribe.OpenAI.APIKey = v
	}
	if v := os.Getenv("MISTRAL_API_KEY"); v != "" {
		c.Transcribe.Mistral.APIKey = v
	}
}

func (c *Config) ResolveOutputDir() string {
	dir := c.Record.OutputDir
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}
	return dir
}
