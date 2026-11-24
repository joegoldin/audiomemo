package main

import (
	"context"
	"github.com/joegoldin/audiomemo/cmd"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/desktopmac"
	"github.com/joegoldin/audiomemo/internal/desktopservice"
	"github.com/joegoldin/audiomemo/internal/meetingcapture"
	"github.com/joegoldin/audiomemo/internal/meetingfinal"
	"github.com/joegoldin/audiomemo/internal/quickscribe"
	"github.com/joegoldin/audiomemo/internal/record"
	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
	"runtime"
)

func init() { cmd.SetDaemonFactory(desktopOptions) }
func desktopOptions(_ context.Context, path string) (desktopservice.Options, error) {
	var c *config.Config
	var err error
	if path == "" {
		c, err = config.Load()
	} else {
		c, err = config.LoadFrom(path)
	}
	if err != nil {
		return desktopservice.Options{}, err
	}
	root := c.ResolveOutputDir()
	opts := meetingfinal.Options{Nemo: c.Transcribe.Nemo, Language: c.Transcribe.Language}
	return desktopservice.Options{Desktop: nativeDesktop(), Paste: c.Desktop.Paste,
		StartQuick: func(ctx context.Context, id, dev string, both bool, emit func(st.Preview), finish func(string)) (st.Quick, error) {
			return quickscribe.Start(ctx, c, id, dev, both, emit, finish)
		},
		StartMeetingPreview: func(ctx context.Context, id, dev string, emit func(st.Preview)) (st.Meeting, error) {
			if dev == "" {
				dev = c.Desktop.MeetingDevice
			}
			inventory, e := record.ListDevices()
			if e != nil {
				return nil, e
			}
			sources, e := record.ResolveSources(c, dev, inventory)
			if e != nil {
				return nil, e
			}
			cap, e := meetingcapture.Start(ctx, root, dev, sources, func(ctx context.Context, s st.Source) (meetingcapture.Stream, error) {
				if runtime.GOOS == "darwin" && s.Device != record.SystemAudioDevice {
					if e := desktopmac.CheckMicrophonePermission(ctx); e != nil {
						return meetingcapture.Stream{}, e
					}
				}
				stream, e := record.OpenMeetingSource(ctx, s)
				if s.Device == record.SystemAudioDevice {
					e = desktopmac.DescribeSystemAudioError(e)
				}
				return stream, e
			})
			if e != nil {
				return nil, e
			}
			p := opts
			p.Session = id
			go meetingfinal.Preview(ctx, cap.Preview(), sources, p, emit)
			return cap, nil
		},
		FinalizeMeeting: func(ctx context.Context, b string) (string, error) { return meetingfinal.Finalize(ctx, b, opts) }, ValidateRetry: func(_ context.Context, b string) (string, error) { return meetingcapture.RetryPath(root, b) }}, nil
}
