package record

import (
	"errors"
	"testing"
)

func TestClosedMicrophoneDoesNotPreventFinalizingBufferedAudio(t *testing.T) {
	lost := errors.New("device lost")
	r := &Recorder{done: make(chan struct{}), exitErr: lost}
	r.stopOnce.Do(func() {})
	close(r.done)
	if err := r.CloseCapture(t.Context()); err != nil {
		t.Fatalf("microphone already closed: %v", err)
	}
	if !errors.Is(r.Wait(), lost) {
		t.Fatal("lost terminal diagnostic")
	}
}
