//go:build darwin && cgo

package record

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework CoreAudio -framework Foundation -Wl,-sectcreate,__TEXT,__info_plist,${SRCDIR}/audio_capture.plist
#include "system_audio_darwin.h"
*/
import "C"

import (
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"
)

func systemAudioAvailable() bool { return C.am_system_audio_available() != 0 }

func startSystemAudio() (*systemAudioCapture, error) {
	var rate C.double
	var channels C.uint
	var message [512]C.char
	capture := C.am_system_audio_start(&rate, &channels, &message[0], C.size_t(len(message)))
	if capture == nil {
		return nil, fmt.Errorf("system audio: %s", C.GoString(&message[0]))
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		C.am_system_audio_free(capture)
		return nil, err
	}
	stop, done := make(chan struct{}), make(chan struct{})
	failed := make(chan struct{})
	var captureErr error
	go func() {
		defer close(done)
		defer writer.Close()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		buffer := make([]byte, 32768)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			for {
				n := int(C.am_system_audio_read(capture, unsafe.Pointer(&buffer[0]), C.size_t(len(buffer))))
				if n < 0 {
					captureErr = fmt.Errorf("system audio capture failed: input format changed or encoder fell behind")
					close(failed)
					return
				}
				if n == 0 {
					break
				}
				if _, err := writer.Write(buffer[:n]); err != nil {
					return // ffmpeg exited or recording was stopped
				}
			}
		}
	}()
	var once sync.Once
	return &systemAudioCapture{
		reader: reader, sampleRate: int(rate), channels: int(channels), failed: failed,
		close: func() error {
			once.Do(func() {
				C.am_system_audio_stop(capture)
				close(stop)
				reader.Close()
				writer.Close()
				<-done
				C.am_system_audio_free(capture)
			})
			return captureErr
		},
	}, nil
}
