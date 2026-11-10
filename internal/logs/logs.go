package logs

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

type Line struct {
	Time  time.Time `json:"time"`
	Text  string    `json:"text"`
	Debug bool      `json:"debug"`
}

// Ring keeps the last N lines in memory.
type Ring struct {
	mu    sync.Mutex
	max   int
	lines []Line
	next  int
	full  bool
}

func NewRing(max int) *Ring {
	return &Ring{max: max, lines: make([]Line, max)}
}

func (r *Ring) Add(l Line) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[r.next] = l
	r.next = (r.next + 1) % r.max
	if r.next == 0 {
		r.full = true
	}
}

// Snapshot returns lines oldest-first; debug lines only when includeDebug.
func (r *Ring) Snapshot(includeDebug bool) []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ordered []Line
	if r.full {
		ordered = append(append(ordered, r.lines[r.next:]...), r.lines[:r.next]...)
	} else {
		ordered = append(ordered, r.lines[:r.next]...)
	}
	if includeDebug {
		return ordered
	}
	out := ordered[:0]
	for _, l := range ordered {
		if !l.Debug {
			out = append(out, l)
		}
	}
	return out
}

// RotatingFile appends to path and rotates to path.1 … path.<keep> when maxBytes would be exceeded.
type RotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	size     int64
}

func OpenRotating(path string, maxBytes int64, keep int) (*RotatingFile, error) {
	rf := &RotatingFile{path: path, maxBytes: maxBytes, keep: keep}
	if err := rf.open(); err != nil {
		return nil, err
	}
	return rf, nil
}

func (rf *RotatingFile) open() error {
	f, err := os.OpenFile(rf.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		return errors.Join(fmt.Errorf("stat log: %w", err), f.Close())
	}
	rf.f, rf.size = f, st.Size()
	return nil
}

func (rf *RotatingFile) rotate() error {
	if err := rf.f.Close(); err != nil {
		return fmt.Errorf("close log: %w", err)
	}
	for i := rf.keep - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", rf.path, i)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, fmt.Sprintf("%s.%d", rf.path, i+1)); err != nil {
				return fmt.Errorf("rotate log: %w", err)
			}
		}
	}
	if err := os.Rename(rf.path, rf.path+".1"); err != nil {
		return fmt.Errorf("rotate log: %w", err)
	}
	return rf.open()
}

func (rf *RotatingFile) Write(p []byte) (int, error) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.size > 0 && rf.size+int64(len(p)) > rf.maxBytes {
		if err := rf.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := rf.f.Write(p)
	rf.size += int64(n)
	return n, err
}

func (rf *RotatingFile) WriteLine(tunnel string, l Line) error {
	_, err := fmt.Fprintf(rf, "%s [%s] %s\n", l.Time.Format(time.RFC3339), tunnel, l.Text)
	return err
}

func (rf *RotatingFile) Close() error {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.f.Close()
}
