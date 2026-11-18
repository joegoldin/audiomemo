//go:build !darwin || !cgo

package desktopmac

import (
	"context"

	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
)

// New returns the desktop adapter. Run reports ErrUnavailable without Darwin cgo.
func New() st.Desktop { return unavailable{} }

type unavailable struct{}

func (unavailable) Run(context.Context, st.Controller) error            { return ErrUnavailable }
func (unavailable) Update(st.Status)                                    {}
func (unavailable) Caption(st.Preview)                                  {}
func (unavailable) Target() string                                      { return "" }
func (unavailable) Deliver(context.Context, string, string, bool) error { return ErrUnavailable }

func CheckMicrophonePermission(context.Context) error { return ErrUnavailable }
