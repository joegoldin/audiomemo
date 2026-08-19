// Command stubffmpeg stands in for ffmpeg in tests that need to drive the
// recorder's behaviour rather than record real audio. It writes a placeholder
// output file, prints the astats RMS lines the recorder parses — loud for
// STUB_LOUD_SECONDS, then digital silence — and exits when the recorder asks
// it to stop by writing "q" to stdin, exactly as ffmpeg does. It honours -t by
// exiting when that many seconds have passed, which is how real ffmpeg ends a
// --max-duration recording.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	args := os.Args[1:]

	// The recorder writes its output path immediately after -y. Without it
	// this is a query invocation such as `ffmpeg -sources pulse`, which must
	// not be mistaken for a recording — the "output path" would be the device
	// name, and the stub would drop a file wherever the test happened to run.
	outputPath := ""
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-y" {
			outputPath = args[i+1]
		}
	}
	if outputPath == "" {
		return
	}
	if err := os.WriteFile(outputPath, []byte("stub recording\n"), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "stubffmpeg:", err)
		os.Exit(1)
	}

	loudFor := time.Second
	if v := os.Getenv("STUB_LOUD_SECONDS"); v != "" {
		if secs, err := strconv.ParseFloat(v, 64); err == nil {
			loudFor = time.Duration(secs * float64(time.Second))
		}
	}

	maxDuration := time.Duration(0)
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "-t" {
			continue
		}
		if secs, err := strconv.ParseFloat(args[i+1], 64); err == nil {
			maxDuration = time.Duration(secs * float64(time.Second))
		}
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || (n > 0 && buf[0] == 'q') {
				return
			}
		}
	}()

	start := time.Now()
	for {
		select {
		case <-stopped:
			return
		default:
		}
		if maxDuration > 0 && time.Since(start) >= maxDuration {
			return
		}
		if time.Since(start) > 30*time.Second {
			return // backstop, so a broken test cannot leave this running
		}
		level := "-18.00"
		if time.Since(start) >= loudFor {
			level = "-inf"
		}
		fmt.Fprintf(os.Stderr, "[Parsed_ametadata_2 @ 0x1] lavfi.astats.Overall.RMS_level=%s\n", level)
		time.Sleep(10 * time.Millisecond)
	}
}
