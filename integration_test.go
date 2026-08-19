package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "audiomemo-inttest-*")
	if err != nil {
		panic(err)
	}
	testBinary = filepath.Join(dir, "audiomemo")
	cmd := exec.Command("go", "build", "-o", testBinary, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("failed to build test binary: " + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(testBinary, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

func runWithStdin(t *testing.T, stdinPath string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	f, ferr := os.Open(stdinPath)
	if ferr != nil {
		t.Fatalf("failed to open stdin file: %v", ferr)
	}
	defer f.Close()
	cmd := exec.Command(testBinary, args...)
	cmd.Stdin = f
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// runWithStubFFmpeg runs the binary with a stand-in ffmpeg ahead of the real
// one on PATH, so tests can drive the recorder's own stop conditions without a
// microphone. loudSeconds is how long the stub "hears" speech before going
// silent.
func runWithStubFFmpeg(t *testing.T, loudSeconds string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	stubDir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(stubDir, "ffmpeg"), "./testdata/stubffmpeg")
	build.Stderr = os.Stderr
	if buildErr := build.Run(); buildErr != nil {
		t.Fatalf("failed to build the ffmpeg stub: %v", buildErr)
	}

	cmd := exec.Command(testBinary, args...)
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_LOUD_SECONDS="+loudSeconds,
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// stubRecordConfig writes a config that keeps a test recording out of the
// user's real output directory.
func stubRecordConfig(t *testing.T) (configPath, outputDir string) {
	t.Helper()
	dir := t.TempDir()
	outputDir = filepath.Join(dir, "recordings")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("failed to create output dir: %v", err)
	}
	configPath = filepath.Join(dir, "config.toml")
	contents := "onboard_version = 1\n\n[record]\noutput_dir = \"" + outputDir + "\"\n"
	if err := os.WriteFile(configPath, []byte(contents), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	return configPath, outputDir
}

func requireWhisperCPP(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("whisper-cli"); err != nil {
		t.Skip("whisper-cli not on PATH")
	}
	// Also need the model file
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "share", "whisper-cpp", "ggml-base.bin"),
		filepath.Join(home, ".local", "share", "whisper", "ggml-base.bin"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	t.Skip("whisper-cpp base model not found")
}

const testAudio = "testdata/test.ogg"

// ---------------------------------------------------------------------------
// Root command
// ---------------------------------------------------------------------------

func TestRootHelp(t *testing.T) {
	stdout, _, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help failed: %v", err)
	}
	if !strings.Contains(stdout, "record") || !strings.Contains(stdout, "transcribe") {
		t.Error("root help should list record and transcribe subcommands")
	}
}

func TestRootUnknownCommand(t *testing.T) {
	_, _, err := run(t, "nonexistent")
	if err == nil {
		t.Error("expected error for unknown subcommand")
	}
}

// ---------------------------------------------------------------------------
// Transcribe: help and flag validation
// ---------------------------------------------------------------------------

func TestTranscribeHelp(t *testing.T) {
	stdout, _, err := run(t, "transcribe", "--help")
	if err != nil {
		t.Fatalf("transcribe --help failed: %v", err)
	}
	for _, flag := range []string{"--backend", "--model", "--language", "--output", "--format", "--verbose"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("help should mention %s", flag)
		}
	}
}

func TestTranscribeMissingFile(t *testing.T) {
	_, _, err := run(t, "transcribe", "/nonexistent/file.ogg")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestTranscribeNoArgs(t *testing.T) {
	_, _, err := run(t, "transcribe")
	if err == nil {
		t.Error("expected error when no file argument given")
	}
}

func TestTranscribeUnknownBackend(t *testing.T) {
	_, stderr, err := run(t, "transcribe", "-b", "notreal", testAudio)
	if err == nil {
		t.Error("expected error for unknown backend")
	}
	if !strings.Contains(stderr, "unknown backend") {
		t.Errorf("error should mention 'unknown backend', got: %s", stderr)
	}
	if !strings.Contains(stderr, "available:") {
		t.Errorf("error should list available backends, got: %s", stderr)
	}
}

// ---------------------------------------------------------------------------
// Transcribe: API backends without keys should fail clearly
// ---------------------------------------------------------------------------

func TestTranscribeDeepgramNoKey(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "")
	t.Setenv("DEEPGRAM_API_KEY_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, stderr, err := run(t, "transcribe", "-b", "deepgram", testAudio)
	if err == nil {
		t.Error("expected error without API key")
	}
	if !strings.Contains(stderr, "API key") {
		t.Errorf("error should mention API key, got: %s", stderr)
	}
}

func TestTranscribeOpenAINoKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, stderr, err := run(t, "transcribe", "-b", "openai", testAudio)
	if err == nil {
		t.Error("expected error without API key")
	}
	if !strings.Contains(stderr, "API key") {
		t.Errorf("error should mention API key, got: %s", stderr)
	}
}

func TestTranscribeMistralNoKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	t.Setenv("MISTRAL_API_KEY_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, stderr, err := run(t, "transcribe", "-b", "mistral", testAudio)
	if err == nil {
		t.Error("expected error without API key")
	}
	if !strings.Contains(stderr, "API key") {
		t.Errorf("error should mention API key, got: %s", stderr)
	}
}

// ---------------------------------------------------------------------------
// Transcribe: whisper-cpp end-to-end (requires whisper-cli + model)
// ---------------------------------------------------------------------------

func TestTranscribeWhisperCPPText(t *testing.T) {
	requireWhisperCPP(t)
	stdout, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", testAudio)
	if err != nil {
		t.Fatalf("transcribe failed: %v\nstderr: %s", err, stderr)
	}
	text := strings.TrimSpace(stdout)
	if text == "" {
		t.Fatal("expected non-empty transcription text on stdout")
	}
	// Without -v, stderr should be silent (no progress, no whisper-cpp output)
	if strings.Contains(stderr, "Transcribing with") {
		t.Error("stderr should not show progress without -v")
	}
	if strings.Contains(stderr, "whisper_") {
		t.Error("stderr should not contain whisper-cpp debug output without -v")
	}
}

func TestTranscribeWhisperCPPJSON(t *testing.T) {
	requireWhisperCPP(t)
	stdout, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", "-f", "json", testAudio)
	if err != nil {
		t.Fatalf("transcribe failed: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		Text     string `json:"text"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, stdout)
	}
	if result.Text == "" {
		t.Error("JSON text field should not be empty")
	}
	if len(result.Segments) == 0 {
		t.Error("JSON should have at least one segment")
	}
	for i, seg := range result.Segments {
		if seg.End <= seg.Start {
			t.Errorf("segment %d: end (%f) should be > start (%f)", i, seg.End, seg.Start)
		}
		if seg.Text == "" {
			t.Errorf("segment %d: text should not be empty", i)
		}
	}
}

func TestTranscribeWhisperCPPSRT(t *testing.T) {
	requireWhisperCPP(t)
	stdout, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", "-f", "srt", testAudio)
	if err != nil {
		t.Fatalf("transcribe failed: %v\nstderr: %s", err, stderr)
	}
	// SRT format: sequence number, timestamp, text
	if !strings.Contains(stdout, "-->") {
		t.Error("SRT output should contain --> timestamp separator")
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "1\n") {
		t.Errorf("SRT should start with sequence number 1, got: %.50s", stdout)
	}
}

func TestTranscribeWhisperCPPVTT(t *testing.T) {
	requireWhisperCPP(t)
	stdout, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", "-f", "vtt", testAudio)
	if err != nil {
		t.Fatalf("transcribe failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.HasPrefix(stdout, "WEBVTT") {
		t.Errorf("VTT output should start with WEBVTT header, got: %.50s", stdout)
	}
	if !strings.Contains(stdout, "-->") {
		t.Error("VTT output should contain --> timestamp separator")
	}
}

func TestTranscribeWhisperCPPWithLanguage(t *testing.T) {
	requireWhisperCPP(t)
	stdout, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", "-l", "en", testAudio)
	if err != nil {
		t.Fatalf("transcribe failed: %v\nstderr: %s", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("expected non-empty output with -l en")
	}
}

func TestTranscribeWhisperCPPWithModel(t *testing.T) {
	requireWhisperCPP(t)
	stdout, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", "-m", "base", testAudio)
	if err != nil {
		t.Fatalf("transcribe failed: %v\nstderr: %s", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("expected non-empty output with -m base")
	}
}

func TestTranscribeWhisperCPPOutputFile(t *testing.T) {
	requireWhisperCPP(t)
	dir := t.TempDir()
	outFile := filepath.Join(dir, "result.txt")
	stdout, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", "-o", outFile, testAudio)
	if err != nil {
		t.Fatalf("transcribe failed: %v\nstderr: %s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout should be empty when -o is used, got: %s", stdout)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Error("output file should not be empty")
	}
}

func TestTranscribeWhisperCPPOutputFileJSON(t *testing.T) {
	requireWhisperCPP(t)
	dir := t.TempDir()
	outFile := filepath.Join(dir, "result.json")
	_, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", "-f", "json", "-o", outFile, testAudio)
	if err != nil {
		t.Fatalf("transcribe failed: %v\nstderr: %s", err, stderr)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if !json.Valid(data) {
		t.Errorf("output file should be valid JSON, got: %.100s", data)
	}
}

func TestTranscribeWhisperCPPStdin(t *testing.T) {
	requireWhisperCPP(t)
	stdout, stderr, err := runWithStdin(t, testAudio, "transcribe", "-b", "whisper-cpp", "-")
	if err != nil {
		t.Fatalf("transcribe from stdin failed: %v\nstderr: %s", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("expected non-empty output from stdin")
	}
}

func TestTranscribeWhisperCPPAllFormatsConsistent(t *testing.T) {
	requireWhisperCPP(t)

	// Get JSON output first as the reference
	jsonOut, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", "-f", "json", testAudio)
	if err != nil {
		t.Fatalf("json transcribe failed: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		Text     string `json:"text"`
		Segments []struct {
			Text string `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &result); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	// Text format should match the JSON text field
	textOut, _, err := run(t, "transcribe", "-b", "whisper-cpp", "-f", "text", testAudio)
	if err != nil {
		t.Fatalf("text transcribe failed: %v", err)
	}
	if strings.TrimSpace(textOut) != strings.TrimSpace(result.Text) {
		t.Errorf("text output doesn't match JSON text field:\ntext: %q\njson: %q", strings.TrimSpace(textOut), result.Text)
	}

	// SRT and VTT should contain each segment's text
	srtOut, _, err := run(t, "transcribe", "-b", "whisper-cpp", "-f", "srt", testAudio)
	if err != nil {
		t.Fatalf("srt transcribe failed: %v", err)
	}
	vttOut, _, err := run(t, "transcribe", "-b", "whisper-cpp", "-f", "vtt", testAudio)
	if err != nil {
		t.Fatalf("vtt transcribe failed: %v", err)
	}
	for i, seg := range result.Segments {
		segText := strings.TrimSpace(seg.Text)
		if segText == "" {
			continue
		}
		if !strings.Contains(srtOut, segText) {
			t.Errorf("SRT missing segment %d text: %q", i, segText)
		}
		if !strings.Contains(vttOut, segText) {
			t.Errorf("VTT missing segment %d text: %q", i, segText)
		}
	}
}

// ---------------------------------------------------------------------------
// Transcribe: whisper auto-detection
// ---------------------------------------------------------------------------

func TestTranscribeWhisperAutoDetect(t *testing.T) {
	requireWhisperCPP(t)
	// --backend whisper should auto-detect whisper-cpp if it's on PATH
	stdout, stderr, err := run(t, "transcribe", "-b", "whisper", testAudio)
	if err != nil {
		t.Fatalf("transcribe -b whisper failed: %v\nstderr: %s", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("expected non-empty output from auto-detected whisper")
	}
	_ = stderr
}

func TestTranscribeAutoDetectNoBackendFlag(t *testing.T) {
	requireWhisperCPP(t)
	// No --backend flag at all; should auto-detect
	stdout, stderr, err := run(t, "transcribe", testAudio)
	if err != nil {
		t.Fatalf("transcribe (auto) failed: %v\nstderr: %s", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("expected non-empty output from auto-detected backend")
	}
}

// ---------------------------------------------------------------------------
// Transcribe: quiet by default, verbose with -v
// ---------------------------------------------------------------------------

func TestTranscribeQuietByDefault(t *testing.T) {
	requireWhisperCPP(t)
	stdout, stderr, err := run(t, "transcribe", "-b", "whisper-cpp", testAudio)
	if err != nil {
		t.Fatalf("transcribe failed: %v", err)
	}

	// stdout: only transcription text
	text := strings.TrimSpace(stdout)
	if text == "" {
		t.Error("stdout should contain transcription text")
	}
	if strings.Contains(stdout, "Transcribing with") {
		t.Error("stdout should not contain progress messages")
	}

	// stderr: should be silent without -v
	if strings.Contains(stderr, "Transcribing with") {
		t.Error("stderr should not contain progress without -v")
	}
	if strings.Contains(stderr, "Done in") {
		t.Error("stderr should not contain timing without -v")
	}
	if strings.Contains(stderr, "whisper_") {
		t.Error("stderr should not contain whisper debug without -v")
	}
	if strings.Contains(stderr, "ffmpeg") {
		t.Error("stderr should not contain ffmpeg output without -v")
	}
}

func TestTranscribeVerbose(t *testing.T) {
	requireWhisperCPP(t)
	stdout, stderr, err := run(t, "transcribe", "-v", "-b", "whisper-cpp", testAudio)
	if err != nil {
		t.Fatalf("transcribe -v failed: %v\nstderr: %s", err, stderr)
	}

	// stdout: still just transcription text
	text := strings.TrimSpace(stdout)
	if text == "" {
		t.Error("stdout should contain transcription text")
	}
	if strings.Contains(stdout, "Transcribing with") {
		t.Error("stdout should not contain progress messages even with -v")
	}

	// stderr: should show progress and backend info with -v
	if !strings.Contains(stderr, "Transcribing with whisper-cpp") {
		t.Errorf("stderr should show backend name with -v, got: %s", stderr)
	}
	if !strings.Contains(stderr, "Done in") {
		t.Error("stderr should show completion time with -v")
	}
	// whisper-cpp model loading should appear
	if !strings.Contains(stderr, "whisper_") {
		t.Error("stderr should contain whisper-cpp debug output with -v")
	}
}

// ---------------------------------------------------------------------------
// Transcribe: custom config file
// ---------------------------------------------------------------------------

func TestTranscribeCustomConfig(t *testing.T) {
	requireWhisperCPP(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	os.WriteFile(configPath, []byte(`
[transcribe]
default_backend = "whisper"
language = "en"

[transcribe.whisper]
model = "base"
`), 0644)

	stdout, stderr, err := run(t, "transcribe", "--config", configPath, testAudio)
	if err != nil {
		t.Fatalf("transcribe with config failed: %v\nstderr: %s", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("expected non-empty output with custom config")
	}
}

// ---------------------------------------------------------------------------
// Record: help and flag validation
// ---------------------------------------------------------------------------

func TestRecordHelp(t *testing.T) {
	stdout, _, err := run(t, "record", "--help")
	if err != nil {
		t.Fatalf("record --help failed: %v", err)
	}
	for _, flag := range []string{"--max-duration", "--format", "--device", "--list-devices",
		"--sample-rate", "--channels", "--name", "--temp", "--transcribe", "--no-live-transcription", "--no-tui"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("help should mention %s", flag)
		}
	}
}

func TestRecordClipsFlag(t *testing.T) {
	stdout, _, err := run(t, "record", "--help")
	if err != nil {
		t.Fatalf("record --help failed: %v", err)
	}
	if !strings.Contains(stdout, "--clips") {
		t.Error("help should mention --clips flag")
	}
	if !strings.Contains(stdout, "-C") {
		t.Error("help should mention -C shorthand")
	}
}

func TestRecordClipsRequiresName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	os.WriteFile(configPath, []byte("onboard_version = 1\n"), 0644)

	_, stderr, err := run(t, "record", "--clips", "--no-tui", "-D", "default", "--config", configPath)
	if err == nil {
		t.Error("clips mode without name should fail")
	}
	if !strings.Contains(stderr, "requires a name") {
		t.Errorf("error should mention name requirement, got: %s", stderr)
	}
}

func TestRecordMultiWordName(t *testing.T) {
	stdout, _, err := run(t, "record", "--help")
	if err != nil {
		t.Fatalf("record --help failed: %v", err)
	}
	if !strings.Contains(stdout, "[name ...]") {
		t.Error("help should show [name ...] for multi-word support")
	}
}

func TestRecordListDevices(t *testing.T) {
	// This may fail on CI without audio devices, but should not crash
	_, _, err := run(t, "record", "--list-devices")
	// We don't check error because there may be no PulseAudio on the test system
	_ = err
}

// ---------------------------------------------------------------------------
// Binary name dispatch (symlink tests)
// ---------------------------------------------------------------------------

func TestBinaryDispatchTranscribe(t *testing.T) {
	requireWhisperCPP(t)
	dir := t.TempDir()
	symlink := filepath.Join(dir, "transcribe")
	if err := os.Symlink(testBinary, symlink); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(symlink, "-b", "whisper-cpp", testAudio)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("symlink transcribe failed: %v\nstderr: %s", err, errBuf.String())
	}
	if strings.TrimSpace(outBuf.String()) == "" {
		t.Error("symlink transcribe should produce output")
	}
}

func TestBinaryDispatchRecord(t *testing.T) {
	dir := t.TempDir()
	symlink := filepath.Join(dir, "record")
	if err := os.Symlink(testBinary, symlink); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(symlink, "--help")
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("symlink record --help failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Record audio") {
		t.Error("symlink 'record' should show record help")
	}
}

func TestBinaryDispatchRect(t *testing.T) {
	dir := t.TempDir()
	symlink := filepath.Join(dir, "rect")
	if err := os.Symlink(testBinary, symlink); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(symlink, "--help")
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("symlink rect --help failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Record audio") {
		t.Error("symlink 'rect' should show record help")
	}
}

func TestBinaryDispatchRecw(t *testing.T) {
	dir := t.TempDir()
	symlink := filepath.Join(dir, "recw")
	if err := os.Symlink(testBinary, symlink); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(symlink, "--help")
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("symlink recw --help failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Record audio") {
		t.Error("symlink 'recw' should show record help")
	}
}

// ---------------------------------------------------------------------------
// Completion subcommand
// ---------------------------------------------------------------------------

func TestCompletionBash(t *testing.T) {
	stdout, _, err := run(t, "completion", "bash")
	if err != nil {
		t.Fatalf("completion bash failed: %v", err)
	}
	if !strings.Contains(stdout, "bash") && !strings.Contains(stdout, "complete") {
		t.Error("bash completion should contain shell completion code")
	}
}

func TestCompletionFish(t *testing.T) {
	stdout, _, err := run(t, "completion", "fish")
	if err != nil {
		t.Fatalf("completion fish failed: %v", err)
	}
	if stdout == "" {
		t.Error("fish completion should not be empty")
	}
}

func TestCompletionZsh(t *testing.T) {
	stdout, _, err := run(t, "completion", "zsh")
	if err != nil {
		t.Fatalf("completion zsh failed: %v", err)
	}
	if stdout == "" {
		t.Error("zsh completion should not be empty")
	}
}

// ---------------------------------------------------------------------------
// Transcribe latest: subcommand tests
// ---------------------------------------------------------------------------

func TestTranscribeLatestHelp(t *testing.T) {
	stdout, _, err := run(t, "transcribe", "latest", "--help")
	if err != nil {
		t.Fatalf("transcribe latest --help failed: %v", err)
	}
	if !strings.Contains(stdout, "newest audio file") {
		t.Error("help should mention newest audio file")
	}
	if !strings.Contains(stdout, "[name]") {
		t.Error("help should show optional name argument")
	}
}

func TestTranscribeLatestNoFiles(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	os.WriteFile(configPath, []byte(`
[record]
output_dir = "`+filepath.Join(dir, "recordings")+`"
`), 0644)
	os.MkdirAll(filepath.Join(dir, "recordings"), 0755)

	_, stderr, err := run(t, "transcribe", "latest", "--config", configPath)
	if err == nil {
		t.Error("expected error when no audio files exist")
	}
	if !strings.Contains(stderr, "no audio files") {
		t.Errorf("error should mention no audio files, got: %s", stderr)
	}
}

func TestTranscribeLatestFindsNewest(t *testing.T) {
	requireWhisperCPP(t)

	dir := t.TempDir()
	recDir := filepath.Join(dir, "recordings")
	os.MkdirAll(recDir, 0755)

	// Copy testAudio as two recordings with different times.
	data, _ := os.ReadFile(testAudio)
	old := filepath.Join(recDir, "recording-old.ogg")
	os.WriteFile(old, []byte("not real audio"), 0644)
	newest := filepath.Join(recDir, "recording-new.ogg")
	os.WriteFile(newest, data, 0644)

	configPath := filepath.Join(dir, "config.toml")
	os.WriteFile(configPath, []byte(`
[record]
output_dir = "`+recDir+`"
`), 0644)

	_, stderr, err := run(t, "transcribe", "latest", "-b", "whisper-cpp", "--config", configPath)
	if err != nil {
		t.Fatalf("transcribe latest failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "recording-new.ogg") {
		t.Errorf("should transcribe the newest file, stderr: %s", stderr)
	}
}

func TestTranscribeLatestWithName(t *testing.T) {
	requireWhisperCPP(t)

	dir := t.TempDir()
	recDir := filepath.Join(dir, "recordings")
	os.MkdirAll(recDir, 0755)

	data, _ := os.ReadFile(testAudio)
	original := filepath.Join(recDir, "recording-2025-01-01T12-00-00.ogg")
	os.WriteFile(original, data, 0644)

	configPath := filepath.Join(dir, "config.toml")
	os.WriteFile(configPath, []byte(`
[record]
output_dir = "`+recDir+`"
`), 0644)

	_, stderr, err := run(t, "transcribe", "latest", "standup", "-b", "whisper-cpp", "--config", configPath)
	if err != nil {
		t.Fatalf("transcribe latest with name failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "standup") {
		t.Errorf("should show renamed file in output, stderr: %s", stderr)
	}

	// Original file should be renamed.
	if _, err := os.Stat(original); err == nil {
		t.Error("original file should have been renamed")
	}
	// Renamed file should exist.
	entries, _ := os.ReadDir(recDir)
	found := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "standup") {
			found = true
		}
	}
	if !found {
		t.Error("renamed file with 'standup' label should exist")
	}
}

// ---------------------------------------------------------------------------
// Record: positional name argument
// ---------------------------------------------------------------------------

func TestRecordHelpShowsPositionalName(t *testing.T) {
	stdout, _, err := run(t, "record", "--help")
	if err != nil {
		t.Fatalf("record --help failed: %v", err)
	}
	if !strings.Contains(stdout, "[name ...]") {
		t.Error("help should show optional [name ...] argument")
	}
}

// ---------------------------------------------------------------------------
// record --stream
// ---------------------------------------------------------------------------

func TestRecordStreamRejectsClips(t *testing.T) {
	_, stderr, err := run(t, "record", "--stream", "--clips", "notes")
	if err == nil {
		t.Fatal("--stream --clips should fail")
	}
	if !strings.Contains(stderr, "--clips") {
		t.Errorf("stderr should name the offending flag, got %q", stderr)
	}
}

func TestRecordStreamRejectsListDevices(t *testing.T) {
	_, stderr, err := run(t, "record", "--stream", "--list-devices")
	if err == nil {
		t.Fatal("--stream --list-devices should fail")
	}
	if !strings.Contains(stderr, "device list") {
		t.Errorf("stderr should point at the alternative, got %q", stderr)
	}
}

// Rejections must not put anything on stdout, or a consumer that started
// parsing before checking the exit status would see a half-stream.
func TestRecordStreamRejectionsKeepStdoutClean(t *testing.T) {
	stdout, _, _ := run(t, "record", "--stream", "--clips", "notes")
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestRecordHelpDocumentsStream(t *testing.T) {
	stdout, _, err := run(t, "record", "--help")
	if err != nil {
		t.Fatalf("record --help failed: %v", err)
	}
	if !strings.Contains(stdout, "--stream") {
		t.Error("record --help does not mention --stream")
	}
	if !strings.Contains(stdout, "newline-delimited JSON") {
		t.Error("the --stream help text should say what it emits")
	}
}

// The regression gate for the flag's whole premise: without --stream, record
// still prints one bare path and nothing else, so `transcribe $(record)` works.
func TestRecordWithoutStreamStillListsDevicesAsText(t *testing.T) {
	stdout, _, err := run(t, "record", "--list-devices")
	if err != nil {
		t.Skipf("no audio device layer available: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			t.Errorf("plain --list-devices emitted JSON: %q", line)
		}
	}
}

// ---------------------------------------------------------------------------
// Stdout contract (--print) and unattended stop conditions
// ---------------------------------------------------------------------------

func TestRecordHelpDocumentsPrintAndStops(t *testing.T) {
	stdout, _, err := run(t, "record", "--help")
	if err != nil {
		t.Fatalf("record --help failed: %v", err)
	}
	for _, flag := range []string{"--print", "--max-duration", "--max-silence", "--silence-threshold"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("help should mention %s", flag)
		}
	}
}

func TestRecordRejectsUnknownPrintMode(t *testing.T) {
	_, stderr, err := run(t, "record", "--print", "transcript")
	if err == nil {
		t.Fatal("--print transcript should fail")
	}
	// The message has to list the alternatives; the user just guessed wrong.
	for _, want := range []string{"path", "text", "both", "none"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("error should list %q, got %q", want, stderr)
		}
	}
}

func TestRecordPrintRejectedWithStream(t *testing.T) {
	_, stderr, err := run(t, "record", "--stream", "--print", "text")
	if err == nil {
		t.Fatal("--stream --print should fail")
	}
	if !strings.Contains(stderr, "--stream") {
		t.Errorf("stderr should name the conflict, got %q", stderr)
	}
}

func TestRecordPrintTextRejectedWithClips(t *testing.T) {
	_, stderr, err := run(t, "record", "--clips", "notes", "--print", "text")
	if err == nil {
		t.Fatal("--clips --print text should fail")
	}
	if !strings.Contains(stderr, "--clips") {
		t.Errorf("stderr should name the conflict, got %q", stderr)
	}
}

func TestRecordPrintPathAcceptedWithClips(t *testing.T) {
	// Accepted at validation, so it fails later for the missing name instead.
	_, stderr, err := run(t, "record", "--clips", "--print", "path", "--no-tui", "-D", "default")
	if err == nil {
		t.Fatal("clips without a name should still fail")
	}
	if !strings.Contains(stderr, "requires a name") {
		t.Errorf("--print path should be accepted with --clips, got %q", stderr)
	}
}

func TestRecordRejectsMalformedStopDurations(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "bare number is not a duration",
			args: []string{"--max-duration", "30"},
			want: "--max-duration",
		},
		{
			name: "unparseable max-silence",
			args: []string{"--max-silence", "soon"},
			want: "--max-silence",
		},
		{
			name: "negative max-duration",
			args: []string{"--max-duration", "-5s"},
			want: "--max-duration",
		},
		{
			// The deprecated spelling is still wired up, and its errors name
			// the flag the user actually typed.
			name: "deprecated duration alias",
			args: []string{"--duration", "soon"},
			want: "--duration",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := run(t, append([]string{"record"}, tt.args...)...)
			if err == nil {
				t.Fatal("malformed duration should fail")
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr should name %s, got %q", tt.want, stderr)
			}
			// Nothing may reach stdout: a caller doing `record | copy` would
			// otherwise put an error message on the clipboard.
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
		})
	}
}

func TestRecordStopConditionsAppearInHelpText(t *testing.T) {
	stdout, _, err := run(t, "record", "--help")
	if err != nil {
		t.Fatalf("record --help failed: %v", err)
	}
	// The deprecated spelling is hidden from help, so nothing points users at it.
	if strings.Contains(stdout, "--duration ") {
		t.Errorf("help should not advertise the deprecated --duration:\n%s", stdout)
	}
}

func TestRecordStopsAfterSilence(t *testing.T) {
	configPath, outputDir := stubRecordConfig(t)

	start := time.Now()
	stdout, stderr, err := runWithStubFFmpeg(t, "1.0",
		"record", "--no-tui", "-D", "default", "--no-live-transcription",
		"--max-silence", "1s", "--print", "path", "--config", configPath, "-n", "silence")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("record failed: %v\nstderr: %s", err, stderr)
	}

	// One second of speech plus one of silence; the generous ceiling is for
	// slow CI, and anything near the stub's 60s backstop means it never fired.
	if elapsed > 20*time.Second {
		t.Errorf("recording ran %v; --max-silence did not stop it", elapsed)
	}
	if !strings.Contains(stderr, "Stopped after") {
		t.Errorf("stderr should report the silence stop, got %q", stderr)
	}
	path := strings.TrimSpace(stdout)
	if !strings.HasPrefix(path, outputDir) {
		t.Errorf("stdout = %q, want a path under %s", stdout, outputDir)
	}
}

// The silence clock starts at the first sound, so a recording that never hears
// anything is not cut short while the speaker reaches for the mic.
func TestRecordSilenceWaitsForSpeech(t *testing.T) {
	configPath, _ := stubRecordConfig(t)

	start := time.Now()
	_, stderr, err := runWithStubFFmpeg(t, "0",
		"record", "--no-tui", "-D", "default", "--no-live-transcription",
		"--max-silence", "1s", "--max-duration", "3s", "--print", "path",
		"--config", configPath, "-n", "nospeech")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("record failed: %v\nstderr: %s", err, stderr)
	}

	// --max-duration is the backstop, and the stub honours it by exiting when
	// asked; what matters is that silence did not claim the stop.
	if strings.Contains(stderr, "Stopped after") {
		t.Errorf("silence stopped a recording with no speech in it: %q", stderr)
	}
	// It ran to --max-duration instead, well past the 1s silence limit that
	// would have ended it had the clock started at t=0.
	if elapsed < 2*time.Second {
		t.Errorf("recording ended after %v, before the silence limit could have applied", elapsed)
	}
}

func TestRecordPipedStdoutOmitsThePath(t *testing.T) {
	configPath, outputDir := stubRecordConfig(t)

	// stdout here is a pipe (the test harness captures it), so --print auto
	// resolves to text. There is no transcript to be had — the stub records
	// nothing transcribable and live transcription is off — so the path must
	// not appear on stdout as a consolation prize.
	stdout, stderr, err := runWithStubFFmpeg(t, "1.0",
		"record", "--no-tui", "-D", "default", "--no-live-transcription",
		"--max-duration", "1s", "--transcribe-args", "--backend whisper-cpp",
		"--config", configPath, "-n", "piped")
	_ = err // a missing whisper backend fails the batch pass; stdout is the point
	if strings.Contains(stdout, outputDir) {
		t.Errorf("piped stdout carried the recording path: %q", stdout)
	}
	if !strings.Contains(stderr, "Recording to") {
		t.Errorf("the path belongs on stderr in headless mode, got %q", stderr)
	}
}
