package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	for _, flag := range []string{"--duration", "--format", "--device", "--list-devices",
		"--sample-rate", "--channels", "--name", "--temp", "--transcribe", "--no-tui"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("help should mention %s", flag)
		}
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

func TestBinaryDispatchRec(t *testing.T) {
	dir := t.TempDir()
	symlink := filepath.Join(dir, "rec")
	if err := os.Symlink(testBinary, symlink); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(symlink, "--help")
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("symlink rec --help failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Record audio") {
		t.Error("symlink 'rec' should show record help")
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
	if !strings.Contains(stdout, "[name]") {
		t.Error("help should show optional [name] argument")
	}
}
