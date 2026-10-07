package voicememo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMatch(t *testing.T) {
	recordings := []Recording{{Name: "Meeting", Path: "/library/one.m4a"}, {Name: "Meeting notes", Path: "/library/two.m4a"}, {Name: "Other", Path: "/library/three.m4a"}}
	for _, tt := range []struct {
		query string
		count int
		path  string
	}{
		{"MEETING", 1, "/library/one.m4a"}, {"meet", 2, ""}, {"two.m4a", 1, "/library/two.m4a"}, {"two", 1, "/library/two.m4a"}, {"absent", 0, ""}, {"", 3, ""},
	} {
		t.Run(tt.query, func(t *testing.T) {
			got := Match(recordings, tt.query)
			if len(got) != tt.count {
				t.Fatalf("got %d matches, want %d", len(got), tt.count)
			}
			if tt.path != "" && got[0].Path != tt.path {
				t.Errorf("got %s, want %s", got[0].Path, tt.path)
			}
		})
	}
	recordings = append(recordings, Recording{Name: "Meeting", Path: "/library/four.m4a"})
	if got := Match(recordings, "Meeting"); len(got) != 2 {
		t.Fatalf("duplicate names must remain ambiguous: %v", got)
	}
}

func TestReadLibrary(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.m4a", "two.M4A", "ignored.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("audio"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "directory.m4a"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := readLibrary(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two regular audio files, got %v", got)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	db := filepath.Join(dir, "CloudRecordings.db")
	sql := `CREATE TABLE ZCLOUDRECORDING (ZCUSTOMLABEL TEXT, ZPATH TEXT, ZDATE REAL);
 INSERT INTO ZCLOUDRECORDING VALUES ('Meeting "quoted"', 'one.m4a', 800000001), ('Older', 'two.M4A', 800000000), ('Cloud only', 'missing.m4a', 800000002);`
	if out, err := exec.Command("/usr/bin/sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("create fixture: %s: %v", out, err)
	}
	got, err = readLibrary(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != `Meeting "quoted"` || got[1].Name != "Older" {
		t.Fatalf("unexpected metadata/order: %v", got)
	}
	if got[0].Date.Unix() != 1778307201 {
		t.Errorf("wrong Apple epoch conversion: %v", got[0].Date)
	}
}

func TestReadLibraryEmptyAndMissing(t *testing.T) {
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if _, err := readLibrary(context.Background(), dir); err == nil {
			t.Errorf("expected error for %s", dir)
		}
	}
}
