package voicememo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Recording struct {
	Name string
	Path string
	Date time.Time
}

// List reads Apple's library without modifying its database or recordings.
func List(ctx context.Context) ([]Recording, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("Voice Memos library access is only supported on macOS; export a recording and use transcribe <file> instead")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dirs := []string{
		filepath.Join(home, "Library/Group Containers/group.com.apple.VoiceMemos.shared/Recordings"),
		filepath.Join(home, "Library/Application Support/com.apple.voicememos/Recordings"),
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, libraryError(err)
		}
		recordings, err := readLibrary(ctx, dir)
		if err != nil {
			return nil, libraryError(err)
		}
		return recordings, nil
	}
	return nil, fmt.Errorf("Voice Memos library not found; open Voice Memos and download your recordings first")
}

func libraryError(err error) error {
	return fmt.Errorf("cannot read Voice Memos library: %w; for permission errors, allow your terminal Full Disk Access in System Settings > Privacy & Security, then restart it; alternatively export the recording and use transcribe <file>", err)
}

func readLibrary(ctx context.Context, dir string) ([]Recording, error) {
	var rows []struct {
		Name string  `json:"name"`
		Path string  `json:"path"`
		Date float64 `json:"date"`
	}
	db := filepath.Join(dir, "CloudRecordings.db")
	if _, err := os.Stat(db); err == nil {
		// Use the system SQLite CLI to avoid a cgo dependency. Read-only mode still
		// sees the WAL, unlike copying just the database while Voice Memos is open.
		out, err := exec.CommandContext(ctx, "/usr/bin/sqlite3", "-readonly", "-json", db,
			"SELECT COALESCE(ZCUSTOMLABEL, '') AS name, COALESCE(ZPATH, '') AS path, COALESCE(ZDATE, 0) AS date FROM ZCLOUDRECORDING").Output()
		if err != nil {
			return nil, fmt.Errorf("read Voice Memos metadata: %w", err)
		}
		if len(out) > 0 {
			if err := json.Unmarshal(out, &rows); err != nil {
				return nil, fmt.Errorf("decode Voice Memos metadata: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	metadata := make(map[string]Recording)
	for _, row := range rows {
		if row.Path == "" {
			continue
		}
		name := filepath.Base(row.Path)
		metadata[name] = Recording{Name: row.Name, Date: time.Unix(978307200+int64(row.Date), 0)}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var recordings []Recording
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".m4a") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		recording := metadata[entry.Name()]
		recording.Path = filepath.Join(dir, entry.Name())
		if recording.Name == "" {
			recording.Name = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		}
		if recording.Date.IsZero() {
			recording.Date = info.ModTime()
		}
		recordings = append(recordings, recording)
	}
	sort.Slice(recordings, func(i, j int) bool {
		if recordings[i].Date.Equal(recordings[j].Date) {
			return recordings[i].Path < recordings[j].Path
		}
		return recordings[i].Date.After(recordings[j].Date)
	})
	if len(recordings) == 0 {
		return nil, fmt.Errorf("no downloaded Voice Memos found; open Voice Memos and download a recording first")
	}
	return recordings, nil
}

// Match prefers exact display names or filenames over substring matches.
func Match(recordings []Recording, query string) []Recording {
	query = strings.ToLower(strings.TrimSpace(query))
	var exact, partial []Recording
	for _, recording := range recordings {
		name := strings.ToLower(recording.Name)
		file := strings.ToLower(filepath.Base(recording.Path))
		if query == name || query == file || query == strings.TrimSuffix(file, filepath.Ext(file)) {
			exact = append(exact, recording)
		} else if strings.Contains(name, query) || strings.Contains(file, query) {
			partial = append(partial, recording)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}
