# audiomemo Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Go CLI providing `record` (with bubbletea TUI) and `transcribe` (multi-backend) as a single busybox-style binary, packaged as a Nix flake.

**Architecture:** Single binary dispatches on argv[0]. `transcribe` is a pure CLI that uploads audio to remote APIs or shells out to whisper. `record` wraps ffmpeg for capture with a bubbletea TUI showing VU meter and ambient animation. Config loaded from XDG TOML with env var and flag overrides.

**Tech Stack:** Go, cobra, bubbletea/bubbles/lipgloss, go-toml/v2, ffmpeg (runtime), Nix flake

---

### Task 0: Create repo and bootstrap Go module

**Files:**

- Create: `/home/joe/Development/audiomemo/`
- Create: `/home/joe/Development/audiomemo/main.go`
- Create: `/home/joe/Development/audiomemo/go.mod`
- Create: `/home/joe/Development/audiomemo/flake.nix`
- Create: `/home/joe/Development/audiomemo/.gitignore`

**Step 1: Create repo directory and init git**

```bash
mkdir -p /home/joe/Development/audiomemo
cd /home/joe/Development/audiomemo
git init
```

**Step 2: Create .gitignore**

```gitignore
/audiomemo
/dist/
*.test
```

**Step 3: Create flake.nix with devShell**

```nix
{
  description = "Audio recording and transcription CLI tools";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        audiomemo = pkgs.buildGoModule {
          pname = "audiomemo";
          version = "0.1.0";
          src = ./.;
          vendorHash = null;
          nativeBuildInputs = [ pkgs.makeWrapper ];
          postInstall = ''
            ln -s $out/bin/audiomemo $out/bin/record
            ln -s $out/bin/audiomemo $out/bin/rec
            ln -s $out/bin/audiomemo $out/bin/transcribe
            wrapProgram $out/bin/audiomemo \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.ffmpeg ]}
          '';
        };
      in
      {
        packages.default = audiomemo;
        packages.audiomemo = audiomemo;
        devShells.default = pkgs.mkShell {
          buildInputs = [ pkgs.go pkgs.gopls pkgs.ffmpeg ];
        };
      }
    ) // {
      overlays.default = final: prev: {
        audiomemo = self.packages.${final.system}.audiomemo;
      };
    };
}
```

**Step 4: Init Go module and install deps**

```bash
cd /home/joe/Development/audiomemo
nix develop --command bash -c '
  go mod init github.com/joegoldin/audiomemo
  go get github.com/spf13/cobra@latest
  go get github.com/pelletier/go-toml/v2@latest
  go get github.com/charmbracelet/bubbletea@latest
  go get github.com/charmbracelet/bubbles@latest
  go get github.com/charmbracelet/lipgloss@latest
'
```

**Step 5: Create stub main.go with argv[0] dispatch**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joegoldin/audiomemo/cmd"
)

func main() {
	name := filepath.Base(os.Args[0])
	switch name {
	case "record", "rec":
		cmd.ExecuteRecord()
	case "transcribe":
		cmd.ExecuteTranscribe()
	default:
		cmd.ExecuteRoot()
	}
}
```

**Step 6: Create minimal cmd stubs so it compiles**

Create `cmd/root.go`:

```go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "audiomemo",
	Short: "Audio recording and transcription tools",
	Long:  "A CLI toolkit for recording audio and transcribing it using local or cloud backends.",
}

func init() {
	rootCmd.AddCommand(recordCmd)
	rootCmd.AddCommand(transcribeCmd)
}

func ExecuteRoot() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

Create `cmd/record.go`:

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var recordCmd = &cobra.Command{
	Use:     "record [flags] [filename]",
	Aliases: []string{"rec"},
	Short:   "Record audio from microphone",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("record: not yet implemented")
		return nil
	},
}

func ExecuteRecord() {
	if err := recordCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

Create `cmd/transcribe.go`:

```go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var transcribeCmd = &cobra.Command{
	Use:   "transcribe [flags] <file>",
	Short: "Transcribe audio to text",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("transcribe: not yet implemented")
		return nil
	},
}

func ExecuteTranscribe() {
	if err := transcribeCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

**Step 7: Build and verify**

```bash
nix develop --command go build -o audiomemo .
./audiomemo --help
```

Expected: Shows help with `record` and `transcribe` subcommands.

**Step 8: Commit**

```bash
git add -A
git commit -m "feat: bootstrap audiomemo repo with Go module, cobra CLI, and nix flake"
```

---

### Task 1: Config loading

**Files:**

- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Create: `config.example.toml`

**Step 1: Write config_test.go**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()
	if cfg.Record.Format != "ogg" {
		t.Errorf("expected default format ogg, got %s", cfg.Record.Format)
	}
	if cfg.Record.SampleRate != 48000 {
		t.Errorf("expected default sample rate 48000, got %d", cfg.Record.SampleRate)
	}
	if cfg.Record.Channels != 1 {
		t.Errorf("expected default channels 1, got %d", cfg.Record.Channels)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`
[record]
format = "wav"
sample_rate = 44100

[transcribe]
default_backend = "deepgram"

[transcribe.deepgram]
api_key = "test-key"
model = "nova-2"
`), 0644)

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Record.Format != "wav" {
		t.Errorf("expected wav, got %s", cfg.Record.Format)
	}
	if cfg.Record.SampleRate != 44100 {
		t.Errorf("expected 44100, got %d", cfg.Record.SampleRate)
	}
	if cfg.Record.Channels != 1 {
		t.Errorf("expected default channels 1, got %d", cfg.Record.Channels)
	}
	if cfg.Transcribe.DefaultBackend != "deepgram" {
		t.Errorf("expected deepgram, got %s", cfg.Transcribe.DefaultBackend)
	}
	if cfg.Transcribe.Deepgram.APIKey != "test-key" {
		t.Errorf("expected test-key, got %s", cfg.Transcribe.Deepgram.APIKey)
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	cfg, err := LoadFrom("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Record.Format != "ogg" {
		t.Errorf("expected default ogg, got %s", cfg.Record.Format)
	}
}

func TestEnvVarOverridesConfig(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "env-key")
	cfg := Default()
	cfg.ApplyEnv()
	if cfg.Transcribe.Deepgram.APIKey != "env-key" {
		t.Errorf("expected env-key, got %s", cfg.Transcribe.Deepgram.APIKey)
	}
}

func TestResolveOutputDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg := Default()
	dir := cfg.ResolveOutputDir()
	expected := filepath.Join(home, "Recordings")
	if dir != expected {
		t.Errorf("expected %s, got %s", expected, dir)
	}
}
```

**Step 2: Run tests, verify they fail**

```bash
nix develop --command go test ./internal/config/ -v
```

**Step 3: Implement config.go**

```go
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
	DefaultBackend string        `toml:"default_backend"`
	Language       string        `toml:"language"`
	OutputFormat   string        `toml:"output_format"`
	Whisper        WhisperConfig `toml:"whisper"`
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
```

**Step 4: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/config/ -v
```

**Step 5: Create config.example.toml at repo root**

```toml
[record]
format = "ogg"
sample_rate = 48000
channels = 1
output_dir = "~/Recordings"
# device = "Built-in Microphone"

[transcribe]
# default_backend = "deepgram"
# language = "en"
# output_format = "text"

[transcribe.whisper]
# model = "base"
# binary = "whisper"

[transcribe.deepgram]
# api_key = ""
# model = "nova-3"

[transcribe.openai]
# api_key = ""
# model = "gpt-4o-transcribe"

[transcribe.mistral]
# api_key = ""
# model = "voxtral-mini-latest"
```

**Step 6: Commit**

```bash
git add -A
git commit -m "feat: config loading with TOML, XDG, env var support"
```

---

### Task 2: Transcriber interface and result types

**Files:**

- Create: `internal/transcribe/transcriber.go`
- Create: `internal/transcribe/result.go`
- Test: `internal/transcribe/result_test.go`

**Step 1: Write result_test.go for format output**

```go
package transcribe

import (
	"strings"
	"testing"
)

func TestResultFormatText(t *testing.T) {
	r := &Result{Text: "Hello world", Language: "en", Duration: 2.5}
	out := r.Format(FormatText)
	if out != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", out)
	}
}

func TestResultFormatSRT(t *testing.T) {
	r := &Result{
		Text: "Hello world",
		Segments: []Segment{
			{Start: 0.0, End: 1.5, Text: "Hello"},
			{Start: 1.5, End: 2.5, Text: "world"},
		},
	}
	out := r.Format(FormatSRT)
	if !strings.Contains(out, "00:00:00,000 --> 00:00:01,500") {
		t.Errorf("expected SRT timestamp, got:\n%s", out)
	}
	if !strings.Contains(out, "1\n") {
		t.Errorf("expected sequence number, got:\n%s", out)
	}
}

func TestResultFormatVTT(t *testing.T) {
	r := &Result{
		Text: "Hello world",
		Segments: []Segment{
			{Start: 0.0, End: 1.5, Text: "Hello"},
		},
	}
	out := r.Format(FormatVTT)
	if !strings.HasPrefix(out, "WEBVTT\n") {
		t.Errorf("expected WEBVTT header, got:\n%s", out)
	}
	if !strings.Contains(out, "00:00:00.000 --> 00:00:01.500") {
		t.Errorf("expected VTT timestamp, got:\n%s", out)
	}
}

func TestResultFormatJSON(t *testing.T) {
	r := &Result{Text: "Hello", Language: "en", Duration: 1.0}
	out := r.Format(FormatJSON)
	if !strings.Contains(out, `"text"`) {
		t.Errorf("expected JSON with text field, got:\n%s", out)
	}
}

func TestResultFormatTextFallsBackWhenNoSegments(t *testing.T) {
	r := &Result{Text: "Hello world"}
	out := r.Format(FormatSRT)
	// With no segments, SRT should create one segment from full text
	if !strings.Contains(out, "Hello world") {
		t.Errorf("expected fallback text, got:\n%s", out)
	}
}
```

**Step 2: Run tests, verify they fail**

```bash
nix develop --command go test ./internal/transcribe/ -v
```

**Step 3: Implement transcriber.go (interface + types)**

```go
package transcribe

import "context"

type OutputFormat string

const (
	FormatText OutputFormat = "text"
	FormatJSON OutputFormat = "json"
	FormatSRT  OutputFormat = "srt"
	FormatVTT  OutputFormat = "vtt"
)

func ParseFormat(s string) OutputFormat {
	switch s {
	case "json":
		return FormatJSON
	case "srt":
		return FormatSRT
	case "vtt":
		return FormatVTT
	default:
		return FormatText
	}
}

type TranscribeOpts struct {
	Model    string
	Language string
	Format   OutputFormat
}

type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error)
	Name() string
}
```

**Step 4: Implement result.go (Result type + format methods)**

```go
package transcribe

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Result struct {
	Text     string    `json:"text"`
	Segments []Segment `json:"segments,omitempty"`
	Language string    `json:"language,omitempty"`
	Duration float64   `json:"duration,omitempty"`
}

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func (r *Result) Format(f OutputFormat) string {
	switch f {
	case FormatJSON:
		return r.formatJSON()
	case FormatSRT:
		return r.formatSRT()
	case FormatVTT:
		return r.formatVTT()
	default:
		return r.Text
	}
}

func (r *Result) formatJSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func (r *Result) segments() []Segment {
	if len(r.Segments) > 0 {
		return r.Segments
	}
	return []Segment{{Start: 0, End: r.Duration, Text: r.Text}}
}

func (r *Result) formatSRT() string {
	var b strings.Builder
	for i, seg := range r.segments() {
		fmt.Fprintf(&b, "%d\n", i+1)
		fmt.Fprintf(&b, "%s --> %s\n", srtTime(seg.Start), srtTime(seg.End))
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(seg.Text))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func (r *Result) formatVTT() string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, seg := range r.segments() {
		fmt.Fprintf(&b, "%s --> %s\n", vttTime(seg.Start), vttTime(seg.End))
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(seg.Text))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func srtTime(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	ms := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func vttTime(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	ms := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
```

**Step 5: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/transcribe/ -v
```

**Step 6: Commit**

```bash
git add -A
git commit -m "feat: transcriber interface and result formatting (text/json/srt/vtt)"
```

---

### Task 3: Whisper backend

**Files:**

- Create: `internal/transcribe/whisper.go`
- Test: `internal/transcribe/whisper_test.go`

**Step 1: Write whisper_test.go**

```go
package transcribe

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestWhisperName(t *testing.T) {
	w := NewWhisper("whisper", "base")
	if w.Name() != "whisper" {
		t.Errorf("expected 'whisper', got %s", w.Name())
	}
}

func TestWhisperBinaryNotFound(t *testing.T) {
	w := NewWhisper("nonexistent-binary-xyz", "base")
	_, err := w.Transcribe(context.Background(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error when binary not found")
	}
}

func TestWhisperFileNotFound(t *testing.T) {
	// Only run if whisper is actually available
	if _, err := exec.LookPath("whisper"); err != nil {
		t.Skip("whisper not on PATH")
	}
	w := NewWhisper("whisper", "base")
	_, err := w.Transcribe(context.Background(), "/nonexistent/file.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestWhisperBuildArgs(t *testing.T) {
	w := NewWhisper("whisper", "base")
	args := w.buildArgs("/tmp/test.wav", TranscribeOpts{
		Model:    "large-v3",
		Language: "en",
	})
	found := map[string]bool{}
	for _, a := range args {
		found[a] = true
	}
	if !found["--model"] || !found["large-v3"] {
		t.Errorf("expected --model large-v3 in args: %v", args)
	}
	if !found["--language"] || !found["en"] {
		t.Errorf("expected --language en in args: %v", args)
	}
	if !found["--output-format"] || !found["json"] {
		t.Errorf("expected --output-format json in args: %v", args)
	}
}
```

**Step 2: Run tests, verify they fail**

**Step 3: Implement whisper.go**

```go
package transcribe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Whisper struct {
	binary       string
	defaultModel string
}

func NewWhisper(binary, defaultModel string) *Whisper {
	return &Whisper{binary: binary, defaultModel: defaultModel}
}

func (w *Whisper) Name() string { return "whisper" }

func (w *Whisper) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if _, err := exec.LookPath(w.binary); err != nil {
		return nil, fmt.Errorf("whisper binary %q not found on PATH: %w", w.binary, err)
	}
	if _, err := os.Stat(audioPath); err != nil {
		return nil, fmt.Errorf("audio file not found: %w", err)
	}

	args := w.buildArgs(audioPath, opts)
	cmd := exec.CommandContext(ctx, w.binary, args...)

	// whisper outputs to a file named <input>.json in the same dir
	// Use a temp dir for output
	tmpDir, err := os.MkdirTemp("", "audiomemo-whisper-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	cmd.Args = append(cmd.Args, "--output_dir", tmpDir)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisper failed: %w", err)
	}

	// Find the output JSON file
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	jsonPath := filepath.Join(tmpDir, base+".json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read whisper output: %w", err)
	}

	return w.parseOutput(data)
}

func (w *Whisper) buildArgs(audioPath string, opts TranscribeOpts) []string {
	model := opts.Model
	if model == "" {
		model = w.defaultModel
	}

	args := []string{
		"--model", model,
		"--output-format", "json",
		audioPath,
	}

	if opts.Language != "" {
		args = append(args[:2], append([]string{"--language", opts.Language}, args[2:]...)...)
	}

	return args
}

type whisperOutput struct {
	Text     string          `json:"text"`
	Segments []whisperSegment `json:"segments"`
	Language string          `json:"language"`
}

type whisperSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func (w *Whisper) parseOutput(data []byte) (*Result, error) {
	var out whisperOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("failed to parse whisper JSON: %w", err)
	}

	result := &Result{
		Text:     strings.TrimSpace(out.Text),
		Language: out.Language,
	}
	for _, seg := range out.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: seg.Start,
			End:   seg.End,
			Text:  strings.TrimSpace(seg.Text),
		})
	}
	if len(result.Segments) > 0 {
		result.Duration = result.Segments[len(result.Segments)-1].End
	}
	return result, nil
}
```

**Step 4: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/transcribe/ -v -run Whisper
```

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: local whisper transcription backend"
```

---

### Task 4: Deepgram backend

**Files:**

- Create: `internal/transcribe/deepgram.go`
- Test: `internal/transcribe/deepgram_test.go`

**Step 1: Write deepgram_test.go**

Test request building and response parsing (no live API calls):

```go
package transcribe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDeepgramName(t *testing.T) {
	d := NewDeepgram("key", "nova-3")
	if d.Name() != "deepgram" {
		t.Errorf("expected 'deepgram', got %s", d.Name())
	}
}

func TestDeepgramNoAPIKey(t *testing.T) {
	d := NewDeepgram("", "nova-3")
	_, err := d.Transcribe(t.Context(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error with empty API key")
	}
}

func TestDeepgramParseResponse(t *testing.T) {
	resp := `{
		"metadata": {"duration": 5.0},
		"results": {
			"channels": [{
				"alternatives": [{
					"transcript": "Hello world",
					"confidence": 0.99
				}]
			}],
			"utterances": [
				{"start": 0.0, "end": 2.5, "transcript": "Hello"},
				{"start": 2.5, "end": 5.0, "transcript": "world"}
			]
		}
	}`

	d := NewDeepgram("key", "nova-3")
	result, err := d.parseResponse([]byte(resp))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result.Text)
	}
	if len(result.Segments) != 2 {
		t.Errorf("expected 2 segments, got %d", len(result.Segments))
	}
	if result.Duration != 5.0 {
		t.Errorf("expected duration 5.0, got %f", result.Duration)
	}
}

func TestDeepgramBuildQueryParams(t *testing.T) {
	d := NewDeepgram("key", "nova-3")
	params := d.buildQuery(TranscribeOpts{Language: "en", Model: "nova-2"})
	if params.Get("model") != "nova-2" {
		t.Errorf("expected nova-2, got %s", params.Get("model"))
	}
	if params.Get("language") != "en" {
		t.Errorf("expected en, got %s", params.Get("language"))
	}
	if params.Get("smart_format") != "true" {
		t.Errorf("expected smart_format true")
	}
}

func TestDeepgramRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-key" {
			t.Errorf("bad auth header: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"duration": 1.0},
			"results": map[string]any{
				"channels": []any{map[string]any{
					"alternatives": []any{map[string]any{
						"transcript": "test",
					}},
				}},
			},
		})
	}))
	defer server.Close()

	d := NewDeepgram("test-key", "nova-3")
	d.baseURL = server.URL

	// Create a temp audio file
	tmp := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(tmp, []byte("fake audio"), 0644)

	result, err := d.Transcribe(t.Context(), tmp, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "test" {
		t.Errorf("expected 'test', got %q", result.Text)
	}
}
```

**Step 2: Run tests, verify they fail**

**Step 3: Implement deepgram.go**

```go
package transcribe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

type Deepgram struct {
	apiKey       string
	defaultModel string
	baseURL      string
}

func NewDeepgram(apiKey, defaultModel string) *Deepgram {
	return &Deepgram{
		apiKey:       apiKey,
		defaultModel: defaultModel,
		baseURL:      "https://api.deepgram.com",
	}
}

func (d *Deepgram) Name() string { return "deepgram" }

func (d *Deepgram) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if d.apiKey == "" {
		return nil, fmt.Errorf("deepgram API key not configured (set DEEPGRAM_API_KEY or config)")
	}

	f, err := os.Open(audioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer f.Close()

	query := d.buildQuery(opts)
	reqURL := fmt.Sprintf("%s/v1/listen?%s", d.baseURL, query.Encode())

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, f)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+d.apiKey)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepgram request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepgram API error (%d): %s", resp.StatusCode, string(body))
	}

	return d.parseResponse(body)
}

func (d *Deepgram) buildQuery(opts TranscribeOpts) url.Values {
	q := url.Values{}
	model := opts.Model
	if model == "" {
		model = d.defaultModel
	}
	q.Set("model", model)
	q.Set("smart_format", "true")
	q.Set("punctuate", "true")
	q.Set("utterances", "true")

	if opts.Language != "" {
		q.Set("language", opts.Language)
	} else {
		q.Set("detect_language", "true")
	}
	return q
}

type deepgramResponse struct {
	Metadata struct {
		Duration float64 `json:"duration"`
	} `json:"metadata"`
	Results struct {
		Channels []struct {
			Alternatives []struct {
				Transcript string `json:"transcript"`
			} `json:"alternatives"`
		} `json:"channels"`
		Utterances []struct {
			Start      float64 `json:"start"`
			End        float64 `json:"end"`
			Transcript string  `json:"transcript"`
		} `json:"utterances"`
	} `json:"results"`
}

func (d *Deepgram) parseResponse(data []byte) (*Result, error) {
	var resp deepgramResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse deepgram response: %w", err)
	}

	result := &Result{Duration: resp.Metadata.Duration}

	if len(resp.Results.Channels) > 0 && len(resp.Results.Channels[0].Alternatives) > 0 {
		result.Text = resp.Results.Channels[0].Alternatives[0].Transcript
	}

	for _, u := range resp.Results.Utterances {
		result.Segments = append(result.Segments, Segment{
			Start: u.Start,
			End:   u.End,
			Text:  u.Transcript,
		})
	}

	return result, nil
}
```

**Step 4: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/transcribe/ -v -run Deepgram
```

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: deepgram transcription backend"
```

---

### Task 5: OpenAI backend

**Files:**

- Create: `internal/transcribe/openai.go`
- Test: `internal/transcribe/openai_test.go`

**Step 1: Write openai_test.go**

```go
package transcribe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAIName(t *testing.T) {
	o := NewOpenAI("key", "gpt-4o-transcribe")
	if o.Name() != "openai" {
		t.Errorf("expected 'openai', got %s", o.Name())
	}
}

func TestOpenAINoAPIKey(t *testing.T) {
	o := NewOpenAI("", "gpt-4o-transcribe")
	_, err := o.Transcribe(t.Context(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error with empty API key")
	}
}

func TestOpenAIParseVerboseResponse(t *testing.T) {
	resp := `{
		"text": "Hello world",
		"language": "en",
		"duration": 3.0,
		"segments": [
			{"start": 0.0, "end": 1.5, "text": "Hello"},
			{"start": 1.5, "end": 3.0, "text": "world"}
		]
	}`
	o := NewOpenAI("key", "gpt-4o-transcribe")
	result, err := o.parseVerboseResponse([]byte(resp))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result.Text)
	}
	if len(result.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(result.Segments))
	}
}

func TestOpenAIRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("bad auth header: %s", r.Header.Get("Authorization"))
		}
		ct := r.Header.Get("Content-Type")
		if !strings.Contains(ct, "multipart/form-data") {
			t.Errorf("expected multipart, got %s", ct)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"text":     "hello",
			"language": "en",
			"duration": 1.0,
		})
	}))
	defer server.Close()

	o := NewOpenAI("test-key", "gpt-4o-transcribe")
	o.baseURL = server.URL

	tmp := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(tmp, []byte("fake"), 0644)

	result, err := o.Transcribe(t.Context(), tmp, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" {
		t.Errorf("expected 'hello', got %q", result.Text)
	}
}
```

**Step 2: Run tests, verify they fail**

**Step 3: Implement openai.go**

```go
package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

type OpenAI struct {
	apiKey       string
	defaultModel string
	baseURL      string
}

func NewOpenAI(apiKey, defaultModel string) *OpenAI {
	return &OpenAI{
		apiKey:       apiKey,
		defaultModel: defaultModel,
		baseURL:      "https://api.openai.com",
	}
}

func (o *OpenAI) Name() string { return "openai" }

func (o *OpenAI) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if o.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key not configured (set OPENAI_API_KEY or config)")
	}

	body, contentType, err := o.buildMultipart(audioPath, opts)
	if err != nil {
		return nil, err
	}

	reqURL := fmt.Sprintf("%s/v1/audio/transcriptions", o.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return o.parseVerboseResponse(respBody)
}

func (o *OpenAI) buildMultipart(audioPath string, opts TranscribeOpts) (*bytes.Buffer, string, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open audio file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, "", err
	}

	model := opts.Model
	if model == "" {
		model = o.defaultModel
	}
	w.WriteField("model", model)
	w.WriteField("response_format", "verbose_json")

	if opts.Language != "" {
		w.WriteField("language", opts.Language)
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}

	return &buf, w.FormDataContentType(), nil
}

type openaiVerboseResponse struct {
	Text     string          `json:"text"`
	Language string          `json:"language"`
	Duration float64         `json:"duration"`
	Segments []openaiSegment `json:"segments"`
}

type openaiSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func (o *OpenAI) parseVerboseResponse(data []byte) (*Result, error) {
	var resp openaiVerboseResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse openai response: %w", err)
	}

	result := &Result{
		Text:     resp.Text,
		Language: resp.Language,
		Duration: resp.Duration,
	}
	for _, seg := range resp.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: seg.Start,
			End:   seg.End,
			Text:  seg.Text,
		})
	}
	return result, nil
}
```

**Step 4: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/transcribe/ -v -run OpenAI
```

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: openai transcription backend"
```

---

### Task 6: Mistral Voxtral backend

**Files:**

- Create: `internal/transcribe/mistral.go`
- Test: `internal/transcribe/mistral_test.go`

**Step 1: Write mistral_test.go**

```go
package transcribe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMistralName(t *testing.T) {
	m := NewMistral("key", "voxtral-mini-latest")
	if m.Name() != "mistral" {
		t.Errorf("expected 'mistral', got %s", m.Name())
	}
}

func TestMistralNoAPIKey(t *testing.T) {
	m := NewMistral("", "voxtral-mini-latest")
	_, err := m.Transcribe(t.Context(), "test.wav", TranscribeOpts{})
	if err == nil {
		t.Error("expected error with empty API key")
	}
}

func TestMistralParseResponse(t *testing.T) {
	resp := `{
		"model": "voxtral-mini-latest",
		"text": "Hello world",
		"language": "en",
		"segments": [
			{"start": 0.0, "end": 1.5, "text": "Hello"},
			{"start": 1.5, "end": 3.0, "text": "world"}
		]
	}`
	m := NewMistral("key", "voxtral-mini-latest")
	result, err := m.parseResponse([]byte(resp))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result.Text)
	}
	if len(result.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(result.Segments))
	}
}

func TestMistralRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("bad auth header: %s", r.Header.Get("Authorization"))
		}
		ct := r.Header.Get("Content-Type")
		if !strings.Contains(ct, "multipart/form-data") {
			t.Errorf("expected multipart, got %s", ct)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"text":     "bonjour",
			"language": "fr",
			"model":    "voxtral-mini-latest",
		})
	}))
	defer server.Close()

	m := NewMistral("test-key", "voxtral-mini-latest")
	m.baseURL = server.URL

	tmp := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(tmp, []byte("fake"), 0644)

	result, err := m.Transcribe(t.Context(), tmp, TranscribeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "bonjour" {
		t.Errorf("expected 'bonjour', got %q", result.Text)
	}
}
```

**Step 2: Run tests, verify they fail**

**Step 3: Implement mistral.go**

```go
package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

type Mistral struct {
	apiKey       string
	defaultModel string
	baseURL      string
}

func NewMistral(apiKey, defaultModel string) *Mistral {
	return &Mistral{
		apiKey:       apiKey,
		defaultModel: defaultModel,
		baseURL:      "https://api.mistral.ai",
	}
}

func (m *Mistral) Name() string { return "mistral" }

func (m *Mistral) Transcribe(ctx context.Context, audioPath string, opts TranscribeOpts) (*Result, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("Mistral API key not configured (set MISTRAL_API_KEY or config)")
	}

	body, contentType, err := m.buildMultipart(audioPath, opts)
	if err != nil {
		return nil, err
	}

	reqURL := fmt.Sprintf("%s/v1/audio/transcriptions", m.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mistral request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mistral API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return m.parseResponse(respBody)
}

func (m *Mistral) buildMultipart(audioPath string, opts TranscribeOpts) (*bytes.Buffer, string, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open audio file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, "", err
	}

	model := opts.Model
	if model == "" {
		model = m.defaultModel
	}
	w.WriteField("model", model)

	if opts.Language != "" {
		w.WriteField("language", opts.Language)
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}

	return &buf, w.FormDataContentType(), nil
}

type mistralResponse struct {
	Model    string           `json:"model"`
	Text     string           `json:"text"`
	Language string           `json:"language"`
	Segments []mistralSegment `json:"segments"`
}

type mistralSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func (m *Mistral) parseResponse(data []byte) (*Result, error) {
	var resp mistralResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse mistral response: %w", err)
	}

	result := &Result{
		Text:     resp.Text,
		Language: resp.Language,
	}
	for _, seg := range resp.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: seg.Start,
			End:   seg.End,
			Text:  seg.Text,
		})
	}
	if len(result.Segments) > 0 {
		result.Duration = result.Segments[len(result.Segments)-1].End
	}
	return result, nil
}
```

**Step 4: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/transcribe/ -v -run Mistral
```

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: mistral voxtral transcription backend"
```

---

### Task 7: Backend dispatcher and auto-detect

**Files:**

- Create: `internal/transcribe/dispatch.go`
- Test: `internal/transcribe/dispatch_test.go`

**Step 1: Write dispatch_test.go**

```go
package transcribe

import (
	"testing"

	"github.com/joegoldin/audiomemo/internal/config"
)

func TestAutoDetectWithExplicitBackend(t *testing.T) {
	cfg := config.Default()
	tr, err := NewDispatcher(cfg, "deepgram")
	if err == nil && tr != nil {
		// Should fail because no API key
	}
	// With key set:
	cfg.Transcribe.Deepgram.APIKey = "test"
	tr, err = NewDispatcher(cfg, "deepgram")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Name() != "deepgram" {
		t.Errorf("expected deepgram, got %s", tr.Name())
	}
}

func TestAutoDetectWithConfigDefault(t *testing.T) {
	cfg := config.Default()
	cfg.Transcribe.DefaultBackend = "openai"
	cfg.Transcribe.OpenAI.APIKey = "test"
	tr, err := NewDispatcher(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Name() != "openai" {
		t.Errorf("expected openai, got %s", tr.Name())
	}
}

func TestAutoDetectScansKeys(t *testing.T) {
	cfg := config.Default()
	cfg.Transcribe.Mistral.APIKey = "test"
	tr, err := NewDispatcher(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Name() != "mistral" {
		t.Errorf("expected mistral, got %s", tr.Name())
	}
}

func TestAutoDetectPriorityOrder(t *testing.T) {
	cfg := config.Default()
	cfg.Transcribe.Deepgram.APIKey = "dg"
	cfg.Transcribe.OpenAI.APIKey = "oai"
	cfg.Transcribe.Mistral.APIKey = "mis"
	tr, err := NewDispatcher(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	// Deepgram should win (first in scan order)
	if tr.Name() != "deepgram" {
		t.Errorf("expected deepgram (highest priority), got %s", tr.Name())
	}
}

func TestAutoDetectNoBackendAvailable(t *testing.T) {
	cfg := config.Default()
	_, err := NewDispatcher(cfg, "")
	if err == nil {
		t.Error("expected error when no backend available")
	}
}
```

**Step 2: Run tests, verify they fail**

**Step 3: Implement dispatch.go**

```go
package transcribe

import (
	"fmt"
	"os/exec"

	"github.com/joegoldin/audiomemo/internal/config"
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

	// Check for local whisper
	binary := cfg.Transcribe.Whisper.Binary
	if _, err := exec.LookPath(binary); err == nil {
		return NewWhisper(binary, cfg.Transcribe.Whisper.Model), nil
	}
	// Try whisper-cpp as fallback
	if _, err := exec.LookPath("whisper-cpp"); err == nil {
		return NewWhisper("whisper-cpp", cfg.Transcribe.Whisper.Model), nil
	}

	return nil, fmt.Errorf("no transcription backend available. Set an API key (DEEPGRAM_API_KEY, OPENAI_API_KEY, MISTRAL_API_KEY) or install whisper locally")
}

func newBackend(cfg *config.Config, name string) (Transcriber, error) {
	switch name {
	case "whisper":
		return NewWhisper(cfg.Transcribe.Whisper.Binary, cfg.Transcribe.Whisper.Model), nil
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
		return nil, fmt.Errorf("unknown backend: %s", name)
	}
}
```

**Step 4: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/transcribe/ -v -run AutoDetect
nix develop --command go test ./internal/transcribe/ -v -run Dispatch
```

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: backend auto-detection and dispatcher"
```

---

### Task 8: Wire up transcribe command

**Files:**

- Modify: `cmd/transcribe.go`

**Step 1: Wire flags and backend dispatch into transcribe command**

Replace `cmd/transcribe.go`:

```go
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/transcribe"
	"github.com/spf13/cobra"
)

var (
	tBackend  string
	tModel    string
	tLanguage string
	tOutput   string
	tFormat   string
	tVerbose  bool
	tConfig   string
)

var transcribeCmd = &cobra.Command{
	Use:   "transcribe [flags] <file>",
	Short: "Transcribe audio to text",
	Long: `Transcribe audio files using local whisper or cloud APIs (Deepgram, OpenAI, Mistral).

By default, auto-detects the best available backend. Use --backend to force a specific one.

Examples:
  transcribe recording.ogg
  transcribe -b deepgram -f srt interview.wav
  transcribe -b whisper -l en lecture.mp3
  cat audio.ogg | transcribe -`,
	Args: cobra.ExactArgs(1),
	RunE: runTranscribe,
}

func init() {
	transcribeCmd.Flags().StringVarP(&tBackend, "backend", "b", "", "transcription backend (whisper, deepgram, openai, mistral)")
	transcribeCmd.Flags().StringVarP(&tModel, "model", "m", "", "model name (backend-specific)")
	transcribeCmd.Flags().StringVarP(&tLanguage, "language", "l", "", "language hint (ISO 639-1)")
	transcribeCmd.Flags().StringVarP(&tOutput, "output", "o", "", "output file (default: stdout)")
	transcribeCmd.Flags().StringVarP(&tFormat, "format", "f", "text", "output format (text, json, srt, vtt)")
	transcribeCmd.Flags().BoolVarP(&tVerbose, "verbose", "v", false, "show progress and timing info")
	transcribeCmd.Flags().StringVar(&tConfig, "config", "", "config file path")
}

func ExecuteTranscribe() {
	if err := transcribeCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runTranscribe(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var cfg *config.Config
	var err error
	if tConfig != "" {
		cfg, err = config.LoadFrom(tConfig)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cfg.ApplyEnv()

	audioPath := args[0]

	// Handle stdin
	if audioPath == "-" {
		tmp, err := bufferStdin()
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		audioPath = tmp
	}

	backend, err := transcribe.NewDispatcher(cfg, tBackend)
	if err != nil {
		return err
	}

	if tVerbose {
		fmt.Fprintf(os.Stderr, "Using backend: %s\n", backend.Name())
	}

	opts := transcribe.TranscribeOpts{
		Model:    tModel,
		Language: tLanguage,
		Format:   transcribe.ParseFormat(tFormat),
	}

	result, err := backend.Transcribe(ctx, audioPath, opts)
	if err != nil {
		return err
	}

	output := result.Format(opts.Format)

	if tOutput != "" {
		return os.WriteFile(tOutput, []byte(output), 0644)
	}

	fmt.Print(output)
	return nil
}

func bufferStdin() (string, error) {
	tmp, err := os.CreateTemp("", "audiomemo-stdin-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, os.Stdin); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}
```

**Step 2: Build and verify help output**

```bash
nix develop --command go build -o audiomemo . && ./audiomemo transcribe --help
```

**Step 3: Commit**

```bash
git add -A
git commit -m "feat: wire transcribe command with flags, backend dispatch, stdin support"
```

---

### Task 9: FFmpeg recorder and device listing

**Files:**

- Create: `internal/record/recorder.go`
- Create: `internal/record/devices.go`
- Test: `internal/record/recorder_test.go`
- Test: `internal/record/devices_test.go`

**Step 1: Write recorder_test.go**

```go
package record

import (
	"runtime"
	"testing"
)

func TestBuildFFmpegArgs(t *testing.T) {
	opts := RecordOpts{
		Device:     "default",
		Format:     "ogg",
		SampleRate: 48000,
		Channels:   1,
		OutputPath: "/tmp/test.ogg",
	}
	args := BuildFFmpegArgs(opts)

	// Should contain the output path as last arg
	if args[len(args)-1] != "/tmp/test.ogg" {
		t.Errorf("expected output path as last arg, got %s", args[len(args)-1])
	}

	// Should contain codec
	found := false
	for _, a := range args {
		if a == "libopus" {
			found = true
		}
	}
	if opts.Format == "ogg" && !found {
		t.Error("expected libopus codec for ogg format")
	}
}

func TestBuildFFmpegArgsWav(t *testing.T) {
	opts := RecordOpts{
		Device:     "default",
		Format:     "wav",
		SampleRate: 44100,
		Channels:   2,
		OutputPath: "/tmp/test.wav",
	}
	args := BuildFFmpegArgs(opts)
	found := false
	for _, a := range args {
		if a == "pcm_s16le" {
			found = true
		}
	}
	if !found {
		t.Error("expected pcm_s16le codec for wav format")
	}
}

func TestInputFormatForPlatform(t *testing.T) {
	f := InputFormat()
	switch runtime.GOOS {
	case "linux":
		if f != "pulse" {
			t.Errorf("expected pulse on linux, got %s", f)
		}
	case "darwin":
		if f != "avfoundation" {
			t.Errorf("expected avfoundation on darwin, got %s", f)
		}
	}
}

func TestCodecForFormat(t *testing.T) {
	tests := []struct {
		format string
		codec  string
	}{
		{"ogg", "libopus"},
		{"wav", "pcm_s16le"},
		{"flac", "flac"},
		{"mp3", "libmp3lame"},
	}
	for _, tt := range tests {
		c := CodecForFormat(tt.format)
		if c != tt.codec {
			t.Errorf("format %s: expected %s, got %s", tt.format, tt.codec, c)
		}
	}
}

func TestGenerateFilename(t *testing.T) {
	name := GenerateFilename("ogg", "meeting")
	if name == "" {
		t.Error("expected non-empty filename")
	}
	if len(name) < 10 {
		t.Error("expected filename with timestamp")
	}
}
```

**Step 2: Write devices_test.go**

```go
package record

import (
	"testing"
)

func TestParseDeviceList(t *testing.T) {
	// Simulated ffmpeg -sources pulse output
	output := `Auto-detected sources for pulse:
  * alsa_output.pci-0000_00_1f.3.analog-stereo.monitor [Monitor of Built-in Audio Analog Stereo]
    alsa_input.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo]
`
	devices := ParseDeviceList(output)
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devices))
	}
	if devices[1].Name != "alsa_input.pci-0000_00_1f.3.analog-stereo" {
		t.Errorf("unexpected device name: %s", devices[1].Name)
	}
	if devices[1].Description != "Built-in Audio Analog Stereo" {
		t.Errorf("unexpected description: %s", devices[1].Description)
	}
	if !devices[0].IsDefault {
		t.Error("expected first device to be default (has *)")
	}
}
```

**Step 3: Run tests, verify they fail**

**Step 4: Implement recorder.go**

```go
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
		"-af", "astats=metadata=1:reset=1,ametadata=print:file=-",
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

func (r *Recorder) Stop() error {
	r.stdin.Write([]byte("q"))
	return <-r.Done
}

func EnsureOutputDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}
```

**Step 5: Implement devices.go**

```go
package record

import (
	"os/exec"
	"regexp"
	"strings"
)

type Device struct {
	Name        string
	Description string
	IsDefault   bool
}

var devicePattern = regexp.MustCompile(`^\s+(\*?)\s*(\S+)\s+\[(.+)\]`)

func ParseDeviceList(output string) []Device {
	var devices []Device
	for _, line := range strings.Split(output, "\n") {
		m := devicePattern.FindStringSubmatch(line)
		if len(m) < 4 {
			continue
		}
		devices = append(devices, Device{
			IsDefault:   m[1] == "*",
			Name:        m[2],
			Description: m[3],
		})
	}
	return devices
}

func ListDevices() ([]Device, error) {
	inputFmt := InputFormat()
	cmd := exec.Command("ffmpeg", "-sources", inputFmt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	return ParseDeviceList(string(out)), nil
}
```

**Step 6: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/record/ -v
```

**Step 7: Commit**

```bash
git add -A
git commit -m "feat: ffmpeg recorder, device listing, level parsing"
```

---

### Task 10: TUI - VU meter component

**Files:**

- Create: `internal/tui/vu.go`
- Test: `internal/tui/vu_test.go`

**Step 1: Write vu_test.go**

```go
package tui

import "testing"

func TestVUMeterRender(t *testing.T) {
	vu := NewVUMeter(10)
	// Silent: should be mostly empty
	out := vu.Render(-60.0)
	if out == "" {
		t.Error("expected non-empty render")
	}

	// Loud: should have more filled blocks
	out2 := vu.Render(-6.0)
	if out2 == "" {
		t.Error("expected non-empty render")
	}
}

func TestVUMeterClamp(t *testing.T) {
	vu := NewVUMeter(10)
	// Very quiet
	out1 := vu.Render(-100.0)
	// Clipping
	out2 := vu.Render(0.0)
	// Both should render without panic
	if out1 == "" || out2 == "" {
		t.Error("expected renders for extreme values")
	}
}

func TestDbToLevel(t *testing.T) {
	// -60dB should be ~0
	l := dbToLevel(-60.0)
	if l < 0 || l > 0.1 {
		t.Errorf("expected near 0, got %f", l)
	}
	// 0dB should be 1.0
	l = dbToLevel(0.0)
	if l != 1.0 {
		t.Errorf("expected 1.0, got %f", l)
	}
}
```

**Step 2: Run tests, verify they fail**

**Step 3: Implement vu.go**

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	vuGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	vuYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308"))
	vuRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	vuDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#404040"))
)

const (
	vuBlock = "████"
	vuEmpty = "┃   "
)

type VUMeter struct {
	height int
}

func NewVUMeter(height int) *VUMeter {
	return &VUMeter{height: height}
}

func dbToLevel(db float64) float64 {
	// Map -60..0 dB to 0..1
	const minDB = -60.0
	if db <= minDB {
		return 0
	}
	if db >= 0 {
		return 1.0
	}
	return (db - minDB) / (0 - minDB)
}

func (v *VUMeter) Render(db float64) string {
	level := dbToLevel(db)
	filled := int(level * float64(v.height))

	var lines []string
	for i := v.height - 1; i >= 0; i-- {
		if i < filled {
			pct := float64(i) / float64(v.height)
			var style lipgloss.Style
			switch {
			case pct >= 0.8:
				style = vuRed
			case pct >= 0.5:
				style = vuYellow
			default:
				style = vuGreen
			}
			lines = append(lines, style.Render(vuBlock))
		} else {
			lines = append(lines, vuDim.Render(vuEmpty))
		}
	}
	return strings.Join(lines, "\n")
}
```

**Step 4: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/tui/ -v -run VU
```

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: vertical VU meter component with color gradient"
```

---

### Task 11: TUI - Ambient animation component

**Files:**

- Create: `internal/tui/animation.go`
- Test: `internal/tui/animation_test.go`

**Step 1: Write animation_test.go**

```go
package tui

import "testing"

func TestAnimationRender(t *testing.T) {
	a := NewAnimation(30, 8)
	out := a.Render(0, 0.5, false)
	if out == "" {
		t.Error("expected non-empty render")
	}
}

func TestAnimationPaused(t *testing.T) {
	a := NewAnimation(30, 8)
	// Render two frames while paused - should be identical
	out1 := a.Render(0, 0.5, true)
	out2 := a.Render(5, 0.5, true)
	if out1 != out2 {
		t.Error("paused animation should produce same output regardless of tick")
	}
}

func TestAnimationRespondToLevel(t *testing.T) {
	a := NewAnimation(30, 8)
	quiet := a.Render(0, 0.0, false)
	loud := a.Render(0, 1.0, false)
	// They should differ since amplitude is modulated
	if quiet == loud {
		t.Error("expected different renders for different levels")
	}
}
```

**Step 2: Run tests, verify they fail**

**Step 3: Implement animation.go**

```go
package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var animDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#7c3aed"))

type Animation struct {
	width     int
	height    int
	pauseTick int
}

func NewAnimation(width, height int) *Animation {
	return &Animation{width: width, height: height}
}

func (a *Animation) Render(tick int, level float64, paused bool) string {
	activeTick := tick
	if paused {
		activeTick = a.pauseTick
	} else {
		a.pauseTick = tick
	}

	// Base amplitude + level modulation
	baseAmp := 0.3 + level*0.7
	centerY := float64(a.height) / 2.0
	phase := float64(activeTick) * 0.15

	grid := make([][]rune, a.height)
	for y := range grid {
		grid[y] = make([]rune, a.width)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}

	for x := 0; x < a.width; x++ {
		xf := float64(x)
		// Combine two sine waves for organic feel
		y1 := math.Sin(xf*0.25+phase) * baseAmp * centerY * 0.6
		y2 := math.Sin(xf*0.4+phase*1.3+1.0) * baseAmp * centerY * 0.3

		py := centerY + y1 + y2
		iy := int(math.Round(py))
		if iy >= 0 && iy < a.height {
			grid[iy][x] = '·'
		}
	}

	var lines []string
	for _, row := range grid {
		lines = append(lines, animDot.Render(string(row)))
	}
	return strings.Join(lines, "\n")
}
```

**Step 4: Run tests, verify they pass**

```bash
nix develop --command go test ./internal/tui/ -v -run Animation
```

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: ambient sine wave animation component"
```

---

### Task 12: TUI - Main model and device picker

**Files:**

- Create: `internal/tui/model.go`
- Create: `internal/tui/devicepicker.go`

**Step 1: Implement model.go (main bubbletea model)**

```go
package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joegoldin/audiomemo/internal/record"
)

type State int

const (
	StateRecording State = iota
	StatePaused
	StateSaved
)

type Model struct {
	state      State
	recorder   *record.Recorder
	opts       record.RecordOpts
	startTime  time.Time
	elapsed    time.Duration
	pauseStart time.Time
	pauseTotal time.Duration
	level      float64
	tick       int
	vu         *VUMeter
	anim       *Animation
	picker     *DevicePicker
	showPicker bool
	err        error
	width      int
	height     int
}

type tickMsg time.Time
type levelMsg float64
type doneMsg error

func NewModel(rec *record.Recorder, opts record.RecordOpts) *Model {
	return &Model{
		state:     StateRecording,
		recorder:  rec,
		opts:      opts,
		startTime: time.Now(),
		vu:        NewVUMeter(10),
		anim:      NewAnimation(30, 8),
		picker:    NewDevicePicker(),
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), listenLevel(m.recorder), listenDone(m.recorder))
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*50, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func listenLevel(rec *record.Recorder) tea.Cmd {
	return func() tea.Msg {
		level, ok := <-rec.Level
		if !ok {
			return nil
		}
		return levelMsg(level)
	}
}

func listenDone(rec *record.Recorder) tea.Cmd {
	return func() tea.Msg {
		err := <-rec.Done
		return doneMsg(err)
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.showPicker {
			return m.updatePicker(msg)
		}
		return m.handleKey(msg)

	case tickMsg:
		if m.state == StateRecording {
			m.elapsed = time.Since(m.startTime) - m.pauseTotal
			m.tick++
		}
		return m, tickCmd()

	case levelMsg:
		m.level = float64(msg)
		return m, listenLevel(m.recorder)

	case doneMsg:
		m.state = StateSaved
		if msg != nil {
			m.err = error(msg)
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
		m.recorder.Stop()
		m.state = StateSaved
		return m, nil

	case key.Matches(msg, key.NewBinding(key.WithKeys("p", " "))):
		if m.state == StateRecording {
			m.state = StatePaused
			m.pauseStart = time.Now()
			m.recorder.Pause()
		} else if m.state == StatePaused {
			m.state = StateRecording
			m.pauseTotal += time.Since(m.pauseStart)
			m.recorder.Pause()
		}
		return m, nil

	case key.Matches(msg, key.NewBinding(key.WithKeys("d"))):
		m.showPicker = true
		return m, nil
	}
	return m, nil
}

func (m *Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc", "q"))):
		m.showPicker = false
	}
	return m, nil
}

var (
	recStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Bold(true)
	pauseStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308")).Bold(true)
	savedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e")).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	infoStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#a1a1aa"))
)

func (m *Model) View() string {
	if m.showPicker {
		return m.picker.View()
	}

	// Status line
	var status string
	switch m.state {
	case StateRecording:
		status = recStyle.Render("● REC")
	case StatePaused:
		status = pauseStyle.Render("⏸ PAUSED")
	case StateSaved:
		status = savedStyle.Render("✓ SAVED")
	}

	dur := formatDuration(m.elapsed)
	info := fmt.Sprintf("%dkHz %s", m.opts.SampleRate/1000, channelStr(m.opts.Channels))
	header := fmt.Sprintf("  %s  %s       %s", status, dur, dimStyle.Render(info))

	// Animation
	paused := m.state != StateRecording
	animLevel := dbToLevel(m.level)
	animView := m.anim.Render(m.tick, animLevel, paused)

	// VU
	vuView := m.vu.Render(m.level)

	// Combine animation and VU side by side
	center := lipgloss.JoinHorizontal(lipgloss.Top, animView, "  ", vuView)

	// Info
	micLine := infoStyle.Render(fmt.Sprintf("  mic: %s", m.opts.Device))
	outLine := infoStyle.Render(fmt.Sprintf("  out: %s", m.opts.OutputPath))

	// Keys
	keys := dimStyle.Render("  [p]ause  [q]uit  [d]evices")

	return lipgloss.JoinVertical(lipgloss.Left,
		header, "", center, "", micLine, outLine, "", keys,
	)
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func channelStr(c int) string {
	if c == 1 {
		return "mono"
	}
	return "stereo"
}
```

**Step 2: Implement devicepicker.go**

```go
package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/joegoldin/audiomemo/internal/record"
)

type DevicePicker struct {
	devices  []record.Device
	cursor   int
	selected int
}

func NewDevicePicker() *DevicePicker {
	return &DevicePicker{}
}

func (p *DevicePicker) SetDevices(devices []record.Device) {
	p.devices = devices
	p.cursor = 0
}

func (p *DevicePicker) View() string {
	title := lipgloss.NewStyle().Bold(true).Render("Select input device:")
	s := title + "\n\n"
	for i, d := range p.devices {
		cursor := "  "
		if i == p.cursor {
			cursor = "> "
		}
		name := d.Description
		if d.IsDefault {
			name += " (default)"
		}
		s += cursor + name + "\n"
	}
	s += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("↑/↓ select  enter confirm  esc cancel")
	return s
}
```

**Step 3: Build to verify compilation**

```bash
nix develop --command go build -o audiomemo .
```

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: bubbletea TUI model with VU meter, animation, device picker"
```

---

### Task 13: Wire up record command

**Files:**

- Modify: `cmd/record.go`

**Step 1: Replace cmd/record.go with full implementation**

```go
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
	"github.com/joegoldin/audiomemo/internal/tui"
	"github.com/spf13/cobra"
)

var (
	rDuration       string
	rFormat         string
	rDevice         string
	rListDevices    bool
	rSampleRate     int
	rChannels       int
	rName           string
	rTemp           bool
	rTranscribe     bool
	rTranscribeArgs string
	rNoTUI          bool
	rConfig         string
)

var recordCmd = &cobra.Command{
	Use:     "record [flags] [filename]",
	Aliases: []string{"rec"},
	Short:   "Record audio from microphone",
	Long: `Record audio from your microphone with a live TUI showing VU meter and animation.

Examples:
  record
  record -n meeting
  record -t
  record -d 5m --no-tui
  record -D "Built-in Microphone" -t --transcribe-args="--backend deepgram"`,
	RunE: runRecord,
}

func init() {
	recordCmd.Flags().StringVarP(&rDuration, "duration", "d", "", "max recording duration (e.g. 5m, 1h30m)")
	recordCmd.Flags().StringVar(&rFormat, "format", "", "output format (ogg, wav, flac, mp3)")
	recordCmd.Flags().StringVarP(&rDevice, "device", "D", "", "input device name or index")
	recordCmd.Flags().BoolVarP(&rListDevices, "list-devices", "L", false, "list available input devices")
	recordCmd.Flags().IntVarP(&rSampleRate, "sample-rate", "r", 0, "sample rate in Hz")
	recordCmd.Flags().IntVarP(&rChannels, "channels", "c", 0, "channel count (1=mono, 2=stereo)")
	recordCmd.Flags().StringVarP(&rName, "name", "n", "", "label for filename")
	recordCmd.Flags().BoolVar(&rTemp, "temp", false, "save to temp directory")
	recordCmd.Flags().BoolVarP(&rTranscribe, "transcribe", "t", false, "transcribe after recording")
	recordCmd.Flags().StringVar(&rTranscribeArgs, "transcribe-args", "", "extra args for transcribe")
	recordCmd.Flags().BoolVar(&rNoTUI, "no-tui", false, "headless mode")
	recordCmd.Flags().StringVar(&rConfig, "config", "", "config file path")
}

func ExecuteRecord() {
	if err := recordCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runRecord(cmd *cobra.Command, args []string) error {
	var cfg *config.Config
	var err error
	if rConfig != "" {
		cfg, err = config.LoadFrom(rConfig)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if rListDevices {
		devices, err := record.ListDevices()
		if err != nil {
			return fmt.Errorf("failed to list devices: %w", err)
		}
		for _, d := range devices {
			def := ""
			if d.IsDefault {
				def = " (default)"
			}
			fmt.Printf("  %s [%s]%s\n", d.Name, d.Description, def)
		}
		return nil
	}

	// Merge config with flags
	format := cfg.Record.Format
	if rFormat != "" {
		format = rFormat
	}
	sampleRate := cfg.Record.SampleRate
	if rSampleRate != 0 {
		sampleRate = rSampleRate
	}
	channels := cfg.Record.Channels
	if rChannels != 0 {
		channels = rChannels
	}
	device := cfg.Record.Device
	if rDevice != "" {
		device = rDevice
	}
	if device == "" {
		device = "default"
	}

	// Determine output path
	var outputPath string
	if len(args) > 0 {
		outputPath = args[0]
	} else {
		var outputDir string
		if rTemp {
			outputDir = os.TempDir()
		} else {
			outputDir = cfg.ResolveOutputDir()
		}
		if err := record.EnsureOutputDir(outputDir); err != nil {
			return fmt.Errorf("failed to create output dir: %w", err)
		}
		outputPath = filepath.Join(outputDir, record.GenerateFilename(format, rName))
	}

	opts := record.RecordOpts{
		Device:     device,
		Format:     format,
		SampleRate: sampleRate,
		Channels:   channels,
		OutputPath: outputPath,
	}

	rec, err := record.Start(opts)
	if err != nil {
		return err
	}

	if rNoTUI {
		fmt.Fprintf(os.Stderr, "Recording to %s (Ctrl+C to stop)...\n", outputPath)
		err := <-rec.Done
		if err != nil {
			return err
		}
	} else {
		model := tui.NewModel(rec, opts)
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "Saved: %s\n", outputPath)

	if rTranscribe {
		return runPostTranscribe(outputPath)
	}

	return nil
}

func runPostTranscribe(audioPath string) error {
	self, err := os.Executable()
	if err != nil {
		self = "transcribe"
	}

	args := []string{audioPath}
	if rTranscribeArgs != "" {
		args = append(strings.Fields(rTranscribeArgs), audioPath)
	}

	transcribeCmd := exec.Command(self, append([]string{"transcribe"}, args...)...)
	transcribeCmd.Stdout = os.Stdout
	transcribeCmd.Stderr = os.Stderr
	return transcribeCmd.Run()
}
```

**Step 2: Build and verify**

```bash
nix develop --command go build -o audiomemo . && ./audiomemo record --help
```

**Step 3: Commit**

```bash
git add -A
git commit -m "feat: wire record command with config, TUI, transcribe piping"
```

---

### Task 14: Add missing os import and final build check

**Step 1: Run full test suite**

```bash
nix develop --command go test ./... -v
```

**Step 2: Fix any compilation issues**

Check for missing imports, fix as needed.

**Step 3: Run go vet and clean up**

```bash
nix develop --command bash -c 'go vet ./... && go mod tidy'
```

**Step 4: Test argv[0] dispatch**

```bash
nix develop --command bash -c '
  go build -o audiomemo .
  ln -sf audiomemo rec
  ln -sf audiomemo record
  ln -sf audiomemo transcribe
  ./audiomemo --help
  ./rec --help
  ./transcribe --help
'
```

**Step 5: Commit**

```bash
git add -A
git commit -m "chore: fix compilation, tidy deps, verify argv[0] dispatch"
```

---

### Task 15: Update flake vendorHash and final nix build

**Step 1: Attempt nix build to get correct vendorHash**

```bash
cd /home/joe/Development/audiomemo
nix build 2>&1 | grep "got:" | head -1
```

**Step 2: Update flake.nix with correct vendorHash**

Replace `vendorHash = null;` with the hash from the error output.

**Step 3: Verify nix build succeeds**

```bash
nix build && ls -la result/bin/
```

Should show: `audiomemo`, `rec`, `record`, `transcribe` symlinks.

**Step 4: Verify wrapped binary has ffmpeg on PATH**

```bash
./result/bin/audiomemo transcribe --help
```

**Step 5: Commit**

```bash
git add flake.nix flake.lock
git commit -m "chore: finalize nix flake with correct vendorHash"
```
