package desktopservice

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	st "github.com/joegoldin/audiomemo/internal/sessiontypes"
)

type fakeMeeting struct{ path string }

func (m fakeMeeting) Stop(context.Context) (string, error) { return m.path, nil }
func (m fakeMeeting) Bundle() string                       { return m.path }

type fakeQuick struct {
	closed atomic.Bool
	result func(context.Context) (string, error)
}

func (q *fakeQuick) CloseCapture(context.Context) error       { q.closed.Store(true); return nil }
func (q *fakeQuick) Stop(ctx context.Context) (string, error) { return q.result(ctx) }
func (q *fakeQuick) Cancel()                                  { q.closed.Store(true) }

type fakeDesktop struct {
	deliveries chan string
	statuses   chan st.Status
}

func (d fakeDesktop) Run(ctx context.Context, _ st.Controller) error { <-ctx.Done(); return nil }
func (d fakeDesktop) Update(s st.Status) {
	if d.statuses != nil {
		d.statuses <- s
	}
}
func (fakeDesktop) Caption(st.Preview) {}
func (fakeDesktop) Target() string     { return "original" }
func (d fakeDesktop) Deliver(ctx context.Context, text, target string, paste bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d.deliveries <- text
	return nil
}

func do(c st.Controller, action string) st.Reply {
	return c.Do(context.Background(), st.Request{Version: st.Version, Action: action})
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out")
		var z T
		return z
	}
}
func newTestController(t *testing.T, opts Options) *controller {
	t.Helper()
	c := newController(context.Background(), opts)
	t.Cleanup(func() {
		if err := c.shutdown(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func TestMeetingWaitPinsJobAndDoesNotOwnCapture(t *testing.T) {
	entered := make(chan string, 1)
	finish := make(chan struct{})
	c := newTestController(t, Options{
		StartMeeting: func(_ context.Context, id, _ string) (st.Meeting, error) { return fakeMeeting{"/meetings/" + id}, nil },
		FinalizeMeeting: func(ctx context.Context, bundle string) (string, error) {
			entered <- bundle
			select {
			case <-finish:
				return "first result", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	})
	first := do(c, "meeting.start")
	if first.ID == "" || first.Status.Capture != "meeting" || do(c, "meeting.start").ID != first.ID {
		t.Fatal(first)
	}
	reply := make(chan st.Reply, 1)
	go func() {
		reply <- c.Do(context.Background(), st.Request{Version: st.Version, Action: "meeting.stop", Wait: true, Print: "text"})
	}()
	path := receive(t, entered)
	second := do(c, "meeting.start")
	if second.ID == first.ID || second.ID == "" || second.Status.JobState != "running" {
		t.Fatal(second)
	}
	close(finish)
	got := receive(t, reply)
	if got.Text != "first result" || got.Path != path || got.Status.Session != second.ID {
		t.Fatal(got)
	}
	if do(c, "dictate.start").Error != string(ErrBusy) {
		t.Fatal("dictation did not observe competing capture")
	}
}

func TestQuickCancelIsScopedToSessionAndAutoFinishDeliversOnce(t *testing.T) {
	delivered := make(chan string, 4)
	started := make(chan string, 4)
	notices := make(chan func(string), 2)
	instances := make(chan *fakeQuick, 2)
	c := newTestController(t, Options{Desktop: fakeDesktop{deliveries: delivered},
		StartQuick: func(_ context.Context, id, _ string, _ func(st.Preview), finish func(string)) (st.Quick, error) {
			notices <- finish
			q := &fakeQuick{result: func(ctx context.Context) (string, error) {
				started <- id
				return "text " + id, nil
			}}
			instances <- q
			return q, nil
		},
	})
	first := do(c, "dictate.start")
	oldFinish, old := receive(t, notices), receive(t, instances)
	if do(c, "dictate.cancel").Error != "" || !old.closed.Load() {
		t.Fatal("cancel failed")
	}
	second := do(c, "dictate.start")
	finish, q := receive(t, notices), receive(t, instances)
	ignored := c.Do(context.Background(), st.Request{Version: st.Version, Action: "dictate.cancel", Session: first.ID})
	if ignored.Error != string(ErrNoSession) || q.closed.Load() {
		t.Fatal(ignored)
	}
	oldFinish("old limit")
	finish("PCM limit reached")
	finish("duplicate")
	if receive(t, started) != second.ID || !q.closed.Load() || receive(t, delivered) != "text "+second.ID {
		t.Fatal("wrong delivery")
	}
	stop := do(c, "dictate.stop")
	if stop.Error != "" || stop.Status.Warning != "PCM limit reached" {
		t.Fatal(stop)
	}
	select {
	case text := <-delivered:
		t.Fatalf("duplicate delivery %q", text)
	default:
	}
}

func TestCancelQueuedQuickDoesNotDeliverOrReplaceLast(t *testing.T) {
	delivered := make(chan string, 4)
	entered := make(chan bool, 1)
	finish := make(chan struct{})
	c := newTestController(t, Options{
		Desktop: fakeDesktop{deliveries: delivered},
		StartQuick: func(_ context.Context, id, device string, _ func(st.Preview), _ func(string)) (st.Quick, error) {
			return &fakeQuick{result: func(ctx context.Context) (string, error) {
				if device == "cancel" {
					entered <- true
					<-ctx.Done()
					// Even an engine which returns late text must not deliver it.
					return "late", nil
				}
				if device == "next" {
					select {
					case <-finish:
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
				return device, nil
			}}, nil
		},
	})
	for _, device := range []string{"first", "cancel", "next"} {
		r := c.Do(t.Context(), st.Request{Version: st.Version, Action: "dictate.start", Device: device})
		if r.Error != "" {
			t.Fatal(r)
		}
		if device == "next" {
			break
		}
		if r := do(c, "dictate.stop"); r.Error != "" {
			t.Fatal(r)
		}
		if device == "first" {
			if receive(t, delivered) != "first" {
				t.Fatal("wrong first delivery")
			}
		}
	}
	receive(t, entered)
	c.mu.Lock()
	id := c.pending[0].captureID
	c.mu.Unlock()
	if r := c.Do(t.Context(), st.Request{Version: st.Version, Action: "dictate.cancel", Session: id}); r.Error != "" {
		t.Fatal(r)
	}
	if r := do(c, "last.copy"); r.Error != "" || r.Text != "" {
		t.Fatal(r)
	}
	if receive(t, delivered) != "first" {
		t.Fatal("canceled result became copy-last")
	}
	if r := do(c, "dictate.stop"); r.Error != "" {
		t.Fatal(r)
	}
	close(finish)
	if receive(t, delivered) != "next" {
		t.Fatal("unexpected delivery")
	}
}

type pendingMeeting struct {
	fakeMeeting
	ready, done chan struct{}
}

func (m pendingMeeting) Ready() <-chan struct{} { return m.ready }
func (m pendingMeeting) Done() <-chan struct{}  { return m.done }
func (m pendingMeeting) Status() st.Status      { return st.Status{Warning: "monitor disconnected"} }

func TestMeetingReadinessAndAutomaticClosure(t *testing.T) {
	m := pendingMeeting{fakeMeeting: fakeMeeting{"/meetings/one"}, ready: make(chan struct{}), done: make(chan struct{})}
	states := make(chan st.Status, 20)
	final := make(chan string, 1)
	c := newTestController(t, Options{
		Desktop:         fakeDesktop{statuses: states},
		StartMeeting:    func(context.Context, string, string) (st.Meeting, error) { return m, nil },
		FinalizeMeeting: func(_ context.Context, b string) (string, error) { final <- b; return "", nil },
	})
	go c.desktopUpdates()
	if r := do(c, "meeting.start"); r.Error != "" || r.Status.Capture != "starting" {
		t.Fatal(r)
	}
	close(m.ready)
	for {
		s := receive(t, states)
		if s.Capture == "meeting" {
			if s.Warning != "monitor disconnected" {
				t.Fatal(s)
			}
			break
		}
	}
	close(m.done)
	if b := receive(t, final); b != m.path {
		t.Fatal(b)
	}
	if s := do(c, "status").Status; s.Capture != "idle" || s.Warning != "monitor disconnected" {
		t.Fatal(s)
	}
}

func TestRetryDeduplicatesAndReservesCapacityForCapture(t *testing.T) {
	c := newTestController(t, Options{
		StartMeeting:    func(context.Context, string, string) (st.Meeting, error) { return fakeMeeting{"/meetings/active"}, nil },
		ValidateRetry:   func(_ context.Context, b string) (string, error) { return "/meetings/" + b, nil },
		FinalizeMeeting: func(ctx context.Context, _ string) (string, error) { <-ctx.Done(); return "", ctx.Err() },
	})
	if r := do(c, "meeting.start"); r.Error != "" {
		t.Fatal(r)
	}
	retry := func(b string) st.Reply {
		return c.Do(t.Context(), st.Request{Version: st.Version, Action: "meeting.retry", Bundle: b})
	}
	if r := retry("active"); r.Error != string(ErrBusy) {
		t.Fatal(r)
	}
	a := retry("a")
	if got := retry("a"); got.Error != "" || a.ID == "" || a.ID != got.ID {
		t.Fatal(a, got)
	}
	for _, b := range []string{"b", "c"} {
		if r := retry(b); r.Error != "" {
			t.Fatal(r)
		}
	}
	if r := retry("d"); r.Error != string(ErrQueueFull) {
		t.Fatal(r)
	}
	if r := do(c, "meeting.stop"); r.Error != "" || r.Path != "/meetings/active" {
		t.Fatal(r)
	}
	if r := do(c, "meeting.start"); r.Error != string(ErrQueueFull) {
		t.Fatal(r)
	}
}

type inFlightDesktop struct {
	fakeDesktop
	entered, release chan struct{}
}

func (d inFlightDesktop) Deliver(context.Context, string, string, bool) error {
	close(d.entered)
	<-d.release
	return nil
}
func TestCannotCancelAfterDesktopHandoff(t *testing.T) {
	d := inFlightDesktop{entered: make(chan struct{}), release: make(chan struct{})}
	c := newTestController(t, Options{Desktop: d, StartQuick: func(context.Context, string, string, func(st.Preview), func(string)) (st.Quick, error) {
		return &fakeQuick{result: func(context.Context) (string, error) { return "finished", nil }}, nil
	}})
	defer close(d.release)
	do(c, "dictate.start")
	do(c, "dictate.stop")
	receive(t, d.entered)
	if r := do(c, "dictate.cancel"); r.Error != string(ErrBusy) {
		t.Fatalf("cancel after handoff = %q, want busy", r.Error)
	}
}
