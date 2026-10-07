package voicememo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	recordings := []Recording{{Name: "Meeting", Path: "/library/one.m4a"}, {Name: "Meeting notes", Path: "/library/two.m4a"}, {Name: "Other", Path: "/library/three.m4a"}}
	for _, tt := range []struct {
		query string
		count int
		path  string
	}{
		{"MEETING", 1, "/library/one.m4a"}, {"meet", 2, ""}, {"two.m4a", 1, "/library/two.m4a"}, {"two", 1, "/library/two.m4a"}, {"absent", 0, ""}, {"", 3, ""}, {"MTNG", 2, ""}, {"to.ma", 1, "/library/two.m4a"},
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
		t.Fatalf("duplicate exact names must remain available in input order: %v", got)
	}
}

func TestMatchTiersAndUnicode(t *testing.T) {
	recordings := []Recording{
		{Name: "Équipe demain", Path: "/library/newest.m4a"},
		{Name: "Équipe", Path: "/library/older.m4a"},
		{Name: "Équipe demain", Path: "/library/oldest.m4a"},
	}
	for _, tt := range []struct {
		query string
		paths []string
	}{
		{"ÉQUIPE", []string{"/library/older.m4a"}},
		{"Équipe d", []string{"/library/newest.m4a", "/library/oldest.m4a"}},
		{"ÉQD", []string{"/library/newest.m4a", "/library/oldest.m4a"}},
		{"DÉQ", nil},
	} {
		got := Match(recordings, tt.query)
		if len(got) != len(tt.paths) {
			t.Fatalf("%q: got %v", tt.query, got)
		}
		for i, path := range tt.paths {
			if got[i].Path != path {
				t.Errorf("%q: match %d = %s, want %s", tt.query, i, got[i].Path, path)
			}
		}
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

func TestReadLibraryDisplayTitle(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS SQLite metadata")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "memo.m4a"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "CloudRecordings.db")
	sql := `CREATE TABLE ZCLOUDRECORDING (ZCUSTOMLABEL TEXT, ZCUSTOMLABELFORSORTING TEXT, ZENCRYPTEDTITLE TEXT, ZPATH TEXT, ZDATE REAL, ZDURATION REAL, ZLOCALDURATION REAL);
 INSERT INTO ZCLOUDRECORDING VALUES ('2026-10-07T21:48:14Z','Sorted meeting title','Pretty meeting title','memo.m4a',800000001,709.55,700);`
	if out, err := exec.Command("/usr/bin/sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	for _, tt := range []struct{ update, want string }{
		{"", "Pretty meeting title"},
		{"UPDATE ZCLOUDRECORDING SET ZENCRYPTEDTITLE = ' ';", "Sorted meeting title"},
		{"UPDATE ZCLOUDRECORDING SET ZCUSTOMLABELFORSORTING = NULL;", "2026-10-07T21:48:14Z"},
		{"UPDATE ZCLOUDRECORDING SET ZCUSTOMLABEL = '';", "memo"},
	} {
		if tt.update != "" {
			if out, err := exec.Command("/usr/bin/sqlite3", db, tt.update).CombinedOutput(); err != nil {
				t.Fatalf("update: %s %v", out, err)
			}
		}
		got, err := readLibrary(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Duration == nil || *got[0].Duration != 709.55 {
			t.Fatalf("missing or incorrect database duration: %v", got[0].Duration)
		}
		if got[0].Name != tt.want {
			t.Errorf("name = %q, want %q", got[0].Name, tt.want)
		}
	}
	for _, update := range []string{"UPDATE ZCLOUDRECORDING SET ZDURATION = NULL;", "UPDATE ZCLOUDRECORDING SET ZLOCALDURATION = NULL;"} {
		if out, err := exec.Command("/usr/bin/sqlite3", db, update).CombinedOutput(); err != nil {
			t.Fatalf("update: %s %v", out, err)
		}
		got, err := readLibrary(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(update, "ZDURATION") {
			if got[0].Duration == nil || *got[0].Duration != 700 {
				t.Fatal("missing local-duration fallback")
			}
		} else if got[0].Duration != nil {
			t.Fatal("absent duration must stay unknown")
		}
	}

}
