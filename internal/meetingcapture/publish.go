package meetingcapture

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

var artifactName = regexp.MustCompile(`^(mix\.wav|transcript\.(json|txt|srt|vtt)|asr-[0-9]{3}-[0-9]{6}\.json)$`)
var tempName = regexp.MustCompile(`^\.write-[A-Z2-7]{26}$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Publication struct {
	root     *os.Root
	tracks   *os.Root
	manifest BundleManifest
	spans    map[string][]Segment
}

// WithFinalization takes exclusive ownership of a closed bundle, recovering a
// crashed writer from surviving complete PCM frames without rewriting originals.
// A callback must not retain the Publication past its return.
func WithFinalization(bundle string, fn func(*Publication) error) error {
	root, err := openBundle(bundle)
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := lockBundle(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	m, err := readManifest(root, filepath.Base(filepath.Clean(bundle)))
	if err != nil {
		return err
	}
	for name, receipt := range m.Artifacts {
		if !artifactName.MatchString(name) || !shaPattern.MatchString(receipt.SHA256) || (receipt.Temp != "" && !tempName.MatchString(receipt.Temp)) {
			return errors.New("invalid derived artifact receipt")
		}
	}
	tracks, err := openTracks(root)
	if err != nil {
		return err
	}
	defer tracks.Close()
	expected := make([][]string, len(m.Tracks))
	for i, track := range m.Tracks {
		expected[i] = track.Files
	}
	recovery := m.State == "capturing"
	if recovery {
		m.State = "interrupted"
		m.Error = "capture ended before durable stop"
		m.DurationMS = 0
	}
	spans, err := discoverSegments(tracks, &m)
	if err != nil {
		return err
	}
	if recovery {
		started, _ := time.Parse(time.RFC3339Nano, m.StartedUTC)
		m.Jobs = map[string]Job{"capture": {State: "interrupted", Error: m.Error}}
		m.EndedUTC = started.Add(time.Duration(m.DurationMS) * time.Millisecond).UTC().Format(time.RFC3339Nano)
		if err := writeManifest(root, &m); err != nil {
			return err
		}
	}
	if !recovery && m.Jobs["capture"].State != "interrupted" {
		for i, track := range m.Tracks {
			if !slices.Equal(expected[i], track.Files) {
				return fmt.Errorf("source files for track %s no longer match the captured bundle", track.ID)
			}
		}
	}
	return fn(&Publication{root: root, tracks: tracks, manifest: m, spans: spans})
}

func openTracks(root *os.Root) (*os.Root, error) {
	st, err := root.Lstat("tracks")
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return nil, errors.New("meeting tracks directory must be private and not a symlink")
	}
	tracks, err := root.OpenRoot("tracks")
	if err != nil {
		return nil, err
	}
	got, err := tracks.Stat(".")
	if err != nil || !os.SameFile(got, st) {
		tracks.Close()
		return nil, errors.New("meeting tracks directory changed while opening")
	}
	return tracks, nil
}

func (p *Publication) Manifest() BundleManifest        { return p.manifest }
func (p *Publication) Segments(track string) []Segment { return p.spans[track] }

// Open safely opens a bundle artifact or a validated original for reading only.
func (p *Publication) Open(name string) (*os.File, error) {
	if _, _, _, _, _, _, ok := segmentName(name); ok {
		return openRegular(p.tracks, filepath.Base(name))
	}
	if !artifactName.MatchString(name) {
		return nil, errors.New("invalid meeting artifact path")
	}
	return openRegular(p.root, name)
}

func (p *Publication) SetJob(name, state, message string) error {
	if p.manifest.Jobs == nil {
		p.manifest.Jobs = make(map[string]Job)
	}
	p.manifest.Jobs[name] = Job{State: state, Error: message}
	return writeManifest(p.root, &p.manifest)
}

func (p *Publication) SetState(state, mix, transcript, message string) error {
	p.manifest.State, p.manifest.Mix, p.manifest.Transcript, p.manifest.Error = state, mix, transcript, message
	return writeManifest(p.root, &p.manifest)
}

func newTemp(root *os.Root) (string, *os.File, error) {
	name := ".write-" + rand.Text()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	return name, f, err
}
func finishFile(f *os.File) error { return errors.Join(f.Sync(), f.Close()) }
func hashFile(f *os.File) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func (p *Publication) verify(name, expected string) (bool, error) {
	f, err := openRegular(p.root, name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	h, err := hashFile(f)
	if err != nil {
		return false, err
	}
	if h != expected {
		return false, fmt.Errorf("meeting artifact %s already exists or was modified; move it aside to regenerate", name)
	}
	return true, nil
}

// Publish links a synced temporary file into place without overwriting an
// existing file. The durable receipt precedes the link, so a retry can finish
// a publication interrupted on either side of that link without trusting edits.
func (p *Publication) Publish(name string, write func(*os.File) error) error {
	if !artifactName.MatchString(name) {
		return errors.New("invalid meeting artifact name")
	}
	if p.manifest.Artifacts == nil {
		p.manifest.Artifacts = make(map[string]Artifact)
	}
	receipt := p.manifest.Artifacts[name]
	exists, err := p.verify(name, receipt.SHA256)
	if err != nil {
		return err
	}
	if exists {
		if receipt.Temp == "" {
			return nil
		}
		return p.complete(name, receipt)
	}
	if receipt.Temp != "" {
		exists, err := p.verify(receipt.Temp, receipt.SHA256)
		if err != nil {
			return err
		}
		if exists {
			return p.link(name, receipt)
		}
	}
	temp, f, err := newTemp(p.root)
	if err != nil {
		return err
	}
	retain := false
	defer func() {
		if !retain {
			_ = p.root.Remove(temp)
		}
	}()
	if err := write(f); err != nil {
		return errors.Join(err, f.Close())
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return errors.Join(err, f.Close())
	}
	sha, err := hashFile(f)
	if err := errors.Join(err, finishFile(f)); err != nil {
		return err
	}
	receipt = Artifact{SHA256: sha, Temp: temp}
	p.manifest.Artifacts[name] = receipt
	retain = true // even a failed directory fsync may have published the receipt
	if err := writeManifest(p.root, &p.manifest); err != nil {
		return err
	}
	return p.link(name, receipt)
}
func (p *Publication) link(name string, receipt Artifact) error {
	if err := p.root.Link(receipt.Temp, name); err != nil {
		return err
	}
	if err := syncDir(p.root); err != nil {
		return err
	}
	return p.complete(name, receipt)
}
func (p *Publication) complete(name string, receipt Artifact) error {
	if err := p.root.Remove(receipt.Temp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	receipt.Temp = ""
	p.manifest.Artifacts[name] = receipt
	return writeManifest(p.root, &p.manifest)
}
