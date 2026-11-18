//go:build darwin && cgo

package desktopmac

import (
	"context"
	"errors"
	"testing"
	"unsafe"
)

func TestNativeTargetFenceRefusesUnsafePaste(t *testing.T) {
	for _, tc := range []struct {
		name                                                    string
		authorized, supplied, found, changed, editable, matches bool
		want                                                    deliveryResult
	}{
		{"same verified field", true, true, true, false, true, true, deliveryOK},
		{"no posting grant", false, true, true, false, true, true, deliveryDenied},
		{"no target", true, false, false, false, false, false, deliveryTargetUnknown},
		{"stale token", true, true, false, false, false, false, deliveryChanged},
		{"target lost while recording", true, true, true, true, true, true, deliveryChanged},
		{"unknown or secure field", true, true, true, false, false, false, deliveryTargetUnknown},
		{"different app, window or field", true, true, true, false, true, false, deliveryChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkTargetFence(tc.authorized, tc.supplied, tc.found, tc.changed, tc.editable, tc.matches); got != tc.want {
				t.Fatalf("decision %v; want %v", got, tc.want)
			}
		})
	}
}

func TestCanceledDeliveryDoesNotStartWhenUIResumes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	u := &uiRun{ctx: context.Background(), calls: make(chan func(unsafe.Pointer))}
	result := make(chan error, 1)
	go func() {
		_, err := uiCall(ctx, u, func(context.Context, unsafe.Pointer) bool {
			t.Error("canceled request reached native operation")
			return true
		})
		result <- err
	}()
	queued := <-u.calls
	cancel()
	queued(nil)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
