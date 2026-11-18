// Package meetingcapture owns source capture and all publication into meeting bundles.
package meetingcapture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/joegoldin/audiomemo/internal/sessiontypes"
)

const manifestName = "manifest.json"
const maxTracks = 32

var trackID = regexp.MustCompile(`^[0-9]{3}$`)
var bundleID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[A-Z2-7]{26}$`)

// BundleManifest extends the wire manifest without changing the shared session contract.
type BundleManifest struct {
	sessiontypes.Manifest
	EndedUTC  string              `json:"ended_utc,omitempty"`
	PCMFormat string              `json:"pcm_format"`
	Jobs      map[string]Job      `json:"jobs,omitempty"`
	Artifacts map[string]Artifact `json:"artifacts,omitempty"`
}

type Job struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type Artifact struct {
	SHA256 string `json:"sha256"`
	Temp   string `json:"pending_temp,omitempty"`
}

func validateSources(sources []sessiontypes.Source) error {
	if len(sources) == 0 || len(sources) > maxTracks {
		return fmt.Errorf("meeting needs 1–%d sources", maxTracks)
	}
	ids, devices := map[string]bool{}, map[string]bool{}
	for _, s := range sources {
		if !trackID.MatchString(s.ID) || ids[s.ID] || s.Device == "" || devices[s.Device] || (s.Role != "us" && s.Role != "them") {
			return fmt.Errorf("invalid or duplicate meeting source %q", s.ID)
		}
		ids[s.ID], devices[s.Device] = true, true
	}
	return nil
}

// OpenBundle rejects a symlink as the bundle itself. os.Root confines all
// further lookups even if a parent is renamed while the bundle is in use.
func openBundle(path string) (*os.Root, error) {
	if !bundleID.MatchString(filepath.Base(path)) || filepath.Base(filepath.Dir(path)) != "meetings" {
		return nil, fmt.Errorf("not a meeting bundle: %s", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errors.New("meeting bundle is not a private directory")
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	current, err := r.Stat(".")
	if err != nil || !os.SameFile(info, current) {
		r.Close()
		return nil, errors.New("meeting bundle changed while opening")
	}
	return r, nil
}

func openRegular(root *os.Root, name string) (*os.File, error) {
	// No FIFO/device opens (even between lstat and open), and no symlinks.
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, fmt.Errorf("not a private regular file: %s", name)
	}
	return f, nil
}

func lockBundle(root *os.Root) (*os.File, error) {
	f, err := openRegular(root, ".lock")
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("meeting bundle is busy (capturing or finalizing)")
		}
		return nil, err
	}
	return f, nil
}

func syncDir(root *os.Root) error {
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func commitTemp(root *os.Root, name string, write func(*os.File) error) error {
	temp, f, err := newTemp(root)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	if err := finishFile(f); err != nil {
		return err
	}
	if err := root.Rename(temp, name); err != nil {
		return err
	}
	return syncDir(root)
}

func writeManifest(root *os.Root, m *BundleManifest) error {
	return commitTemp(root, manifestName, func(f *os.File) error {
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	})
}

func readManifest(root *os.Root, id string) (BundleManifest, error) {
	var m BundleManifest
	f, err := openRegular(root, manifestName)
	if err != nil {
		return m, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 32<<20))
	if err := dec.Decode(&m); err != nil {
		return m, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return m, errors.New("invalid manifest trailer")
	}
	switch m.State {
	case "capturing", "captured", "interrupted", "finalizing", "ready", "partial", "failed":
	default:
		return m, errors.New("unknown meeting state")
	}
	if m.Version != sessiontypes.Version || m.ID != id || m.DurationMS < 0 || m.DurationMS > maxTimelineMS {
		return m, errors.New("invalid meeting manifest version, id or duration")
	}
	if _, err := time.Parse(time.RFC3339Nano, m.StartedUTC); err != nil {
		return m, fmt.Errorf("bad meeting start: %w", err)
	}
	sources := make([]sessiontypes.Source, len(m.Tracks))
	for i, t := range m.Tracks {
		sources[i] = t.Source
		for _, name := range t.Files {
			id, _, _, _, _, _, ok := segmentName(name)
			if !ok || id != t.ID {
				return m, errors.New("unsafe or incorrectly assigned source path")
			}
		}
	}
	if m.Mix != "" && m.Mix != "mix.wav" || m.Transcript != "" && m.Transcript != "transcript.txt" {
		return m, errors.New("unsafe derived path")
	}
	return m, validateSources(sources)
}

// RetryPath admits only an unlocked, valid bundle immediately under this
// recording directory. The finalizer takes the lock again before writing.
func RetryPath(output, bundle string) (string, error) {
	base, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	parent := filepath.Join(base, "meetings")
	path, err := filepath.Abs(bundle)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if filepath.Dir(canonical) != parent || filepath.Base(path) != filepath.Base(canonical) {
		return "", errors.New("retry requires a bundle inside the configured meetings folder")
	}
	r, err := openBundle(canonical)
	if err != nil {
		return "", err
	}
	defer r.Close()
	lock, err := lockBundle(r)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	_, err = readManifest(r, filepath.Base(canonical))
	return canonical, err
}
