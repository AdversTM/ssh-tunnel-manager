package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRingKeepsLastN(t *testing.T) {
	r := NewRing(2000)
	for i := 0; i < 2500; i++ {
		r.Add(Line{Text: fmt.Sprint(i)})
	}
	got := r.Snapshot(true)
	if len(got) != 2000 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Text != "500" || got[1999].Text != "2499" {
		t.Fatalf("first=%s last=%s", got[0].Text, got[1999].Text)
	}
}

func TestRingPartialAndDebugFilter(t *testing.T) {
	r := NewRing(10)
	r.Add(Line{Text: "a"})
	r.Add(Line{Text: "debug1: x", Debug: true})
	r.Add(Line{Text: "b"})
	if got := r.Snapshot(true); len(got) != 3 {
		t.Fatalf("verbose len=%d", len(got))
	}
	got := r.Snapshot(false)
	if len(got) != 2 || got[0].Text != "a" || got[1].Text != "b" {
		t.Fatalf("filtered=%v", got)
	}
	if again := r.Snapshot(true); len(again) != 3 || again[1].Text != "debug1: x" {
		t.Fatalf("filtering must not mutate ring: %v", again)
	}
}

func TestRotatingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	rf, err := OpenRotating(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	line := Line{Time: time.Unix(0, 0).UTC(), Text: strings.Repeat("x", 40)}
	for i := 0; i < 20; i++ {
		if err := rf.WriteLine("t", line); err != nil {
			t.Fatal(err)
		}
	}
	if err := rf.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("want app.log + 3 rotated, got %v", files)
	}
	if _, err := os.Stat(path + ".4"); !os.IsNotExist(err) {
		t.Fatalf(".4 must not exist: %v", err)
	}
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() > 100 {
			t.Fatalf("%s size %d > max", f, st.Size())
		}
	}
}
