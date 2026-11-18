//go:build !linux && !darwin

package desktopservice

import (
	"context"
	"errors"

	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
)

var errUnsupported = errors.New("desktop controls require Linux or macOS")

func Run(context.Context, Options) error { return errUnsupported }

type Client struct{ RuntimeDir string }

func (Client) Do(context.Context, st.Request) (st.Reply, error) { return st.Reply{}, errUnsupported }
