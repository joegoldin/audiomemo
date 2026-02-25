package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// CurrentOnboardVersion is the latest onboarding schema version. Bump this
// when the onboarding flow changes and existing users should re-onboard.
const CurrentOnboardVersion = 1

type Config struct {
	OnboardVersion int                 `toml:"onboard_version"`
	Record         RecordConfig        `toml:"record"`
	Devices        map[string]string   `toml:"devices"`
	DeviceGroups   map[string][]string `toml:"device_groups"`
	Transcribe     TranscribeConfig    `toml:"transcribe"`
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
	Model       string `toml:"model"`
	Binary      string `toml:"binary"`
	HFToken     string `toml:"hf_token"`
	HFTokenFile string `toml:"hf_token_file"`
	Diarize     bool   `toml:"diarize"`
}

type DeepgramConfig struct {
	APIKey      string `toml:"api_key"`
	APIKeyFile  string `toml:"api_key_file"`
	Model       string `toml:"model"`
	Diarize     bool   `toml:"diarize"`
	SmartFormat bool   `toml:"smart_format"`
	Punctuate   bool   `toml:"punctuate"`
	FillerWords bool   `toml:"filler_words"`
	Numerals    bool   `toml:"numerals"`
}

type OpenAIConfig struct {
	APIKey     string `toml:"api_key"`
	APIKeyFile string `toml:"api_key_file"`
	Model      string `toml:"model"`
}

type MistralConfig struct {
	APIKey     string `toml:"api_key"`
	APIKeyFile string `toml:"api_key_file"`
	Model      string `toml:"model"`
}

func Default() *Config {
	return &Config{
		Record: RecordConfig{
			Format:     "ogg",
			SampleRate: 48000,
			Channels:   1,
			OutputDir:  "~/Recordings",
		},
		Devices:      map[string]string{},
		DeviceGroups: map[string][]string{},
		Transcribe: TranscribeConfig{
			OutputFormat: "text",
			Whisper:      WhisperConfig{Model: "base", Binary: "whisper"},
			Deepgram:     DeepgramConfig{Model: "nova-3", SmartFormat: true, Diarize: true, Punctuate: true, FillerWords: true, Numerals: true},
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
	return LoadFrom(filepath.Join(configDir, "audiomemo", "config.toml"))
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
	// Direct env vars (highest priority).
	if v := os.Getenv("DEEPGRAM_API_KEY"); v != "" {
		c.Transcribe.Deepgram.APIKey = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		c.Transcribe.OpenAI.APIKey = v
	}
	if v := os.Getenv("MISTRAL_API_KEY"); v != "" {
		c.Transcribe.Mistral.APIKey = v
	}
	if v := os.Getenv("HF_TOKEN"); v != "" && c.Transcribe.Whisper.HFToken == "" {
		c.Transcribe.Whisper.HFToken = v
	}

	// Env var file paths (second priority).
	if c.Transcribe.Deepgram.APIKey == "" {
		if v := os.Getenv("DEEPGRAM_API_KEY_FILE"); v != "" {
			c.Transcribe.Deepgram.APIKey = readKeyFile(v)
		}
	}
	if c.Transcribe.OpenAI.APIKey == "" {
		if v := os.Getenv("OPENAI_API_KEY_FILE"); v != "" {
			c.Transcribe.OpenAI.APIKey = readKeyFile(v)
		}
	}
	if c.Transcribe.Mistral.APIKey == "" {
		if v := os.Getenv("MISTRAL_API_KEY_FILE"); v != "" {
			c.Transcribe.Mistral.APIKey = readKeyFile(v)
		}
	}
	if c.Transcribe.Whisper.HFToken == "" {
		if v := os.Getenv("HF_TOKEN_FILE"); v != "" {
			c.Transcribe.Whisper.HFToken = readKeyFile(v)
		}
	}

	// Config file paths (lowest priority).
	if c.Transcribe.Deepgram.APIKey == "" && c.Transcribe.Deepgram.APIKeyFile != "" {
		c.Transcribe.Deepgram.APIKey = readKeyFile(c.Transcribe.Deepgram.APIKeyFile)
	}
	if c.Transcribe.OpenAI.APIKey == "" && c.Transcribe.OpenAI.APIKeyFile != "" {
		c.Transcribe.OpenAI.APIKey = readKeyFile(c.Transcribe.OpenAI.APIKeyFile)
	}
	if c.Transcribe.Mistral.APIKey == "" && c.Transcribe.Mistral.APIKeyFile != "" {
		c.Transcribe.Mistral.APIKey = readKeyFile(c.Transcribe.Mistral.APIKeyFile)
	}
	if c.Transcribe.Whisper.HFToken == "" && c.Transcribe.Whisper.HFTokenFile != "" {
		c.Transcribe.Whisper.HFToken = readKeyFile(c.Transcribe.Whisper.HFTokenFile)
	}
}

// readKeyFile reads a file and returns its trimmed contents, or empty string on error.
func readKeyFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// NeedsOnboarding reports whether the interactive onboarding flow should be
// shown. It returns false when the user has already completed the current
// onboarding version, or when a device and at least one alias are already
// configured (pre-onboarding setup).
func (c *Config) NeedsOnboarding() bool {
	if c.OnboardVersion >= CurrentOnboardVersion {
		return false
	}
	if c.Record.Device != "" && len(c.Devices) > 0 {
		return false
	}
	return true
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

// defaultConfigPath returns the default XDG config path for the config file.
func defaultConfigPath() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine config path: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "audiomemo", "config.toml"), nil
}

// Save writes the config to the default XDG config path.
func (c *Config) Save() error {
	path, err := defaultConfigPath()
	if err != nil {
		return err
	}
	return c.SaveTo(path)
}

// SaveTo writes the config to the specified path, creating parent directories
// as needed.
func (c *Config) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// ResolveDevice resolves a device name through groups and aliases.
// Resolution order:
//  1. Empty name returns ["default"]
//  2. Check DeviceGroups: resolve each alias in the group via Devices map
//  3. Check Devices: return the raw device name
//  4. Otherwise treat as raw device name and return as-is
func (c *Config) ResolveDevice(name string) ([]string, error) {
	if name == "" {
		return []string{"default"}, nil
	}

	// Check device groups first.
	if aliases, ok := c.DeviceGroups[name]; ok {
		devices := make([]string, 0, len(aliases))
		for _, alias := range aliases {
			raw, ok := c.Devices[alias]
			if !ok {
				return nil, fmt.Errorf("device group %q references unknown alias %q", name, alias)
			}
			devices = append(devices, raw)
		}
		return devices, nil
	}

	// Check device aliases.
	if raw, ok := c.Devices[name]; ok {
		return []string{raw}, nil
	}

	// Treat as raw device name.
	return []string{name}, nil
}
