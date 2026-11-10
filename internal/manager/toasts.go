package manager

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"ssh-tunnel-manager/internal/supervisor"
)

type Toast struct {
	Title string
	Body  string
}

type toastKind int

const (
	toastDropped toastKind = iota
	toastRestored
	toastFailed
)

type toastEvent struct {
	id, name string
	kind     toastKind
	detail   string
}

// toaster turns state transitions into notifications: Up→Backoff (dropped),
// back to Up after a drop (restored) and →Error. Events arriving within delay
// of each other are sent as one notification.
type toaster struct {
	delay   time.Duration
	enabled func() bool
	send    func(Toast)

	mu      sync.Mutex
	prev    map[string]supervisor.State
	dropped map[string]bool
	pending []toastEvent
	timer   *time.Timer
}

func newToaster(delay time.Duration, enabled func() bool, send func(Toast)) *toaster {
	return &toaster{delay: delay, enabled: enabled, send: send, prev: map[string]supervisor.State{}, dropped: map[string]bool{}}
}

func (t *toaster) observe(name string, st supervisor.Status) {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev := t.prev[st.ID]
	t.prev[st.ID] = st.State
	if prev == st.State {
		return
	}
	switch {
	case prev == supervisor.Up && st.State == supervisor.Backoff:
		t.dropped[st.ID] = true
		t.queueLocked(toastEvent{id: st.ID, name: name, kind: toastDropped})
	case st.State == supervisor.Up && t.dropped[st.ID]:
		delete(t.dropped, st.ID)
		t.queueLocked(toastEvent{id: st.ID, name: name, kind: toastRestored})
	case st.State == supervisor.Error && st.ErrorKind != "host-key-unknown":
		delete(t.dropped, st.ID)
		t.queueLocked(toastEvent{id: st.ID, name: name, kind: toastFailed, detail: errorLabel(st.ErrorKind)})
	case st.State == supervisor.Stopped:
		delete(t.dropped, st.ID)
	}
}

func (t *toaster) forget(id string) {
	t.mu.Lock()
	delete(t.prev, id)
	delete(t.dropped, id)
	t.mu.Unlock()
}

func (t *toaster) queueLocked(e toastEvent) {
	// A drop and its recovery inside one batch is a blip: report neither.
	if e.kind == toastRestored {
		if i := slices.IndexFunc(t.pending, func(p toastEvent) bool { return p.id == e.id && p.kind == toastDropped }); i >= 0 {
			t.pending = slices.Delete(t.pending, i, i+1)
			return
		}
	}
	t.pending = append(t.pending, e)
	if t.timer == nil {
		t.timer = time.AfterFunc(t.delay, t.flush)
	}
}

func (t *toaster) flush() {
	t.mu.Lock()
	events := t.pending
	t.pending, t.timer = nil, nil
	t.mu.Unlock()
	if len(events) == 0 || !t.enabled() {
		return
	}
	t.send(composeToast(events))
}

func names(events []toastEvent, kind toastKind) []string {
	var out []string
	for _, e := range events {
		if e.kind == kind {
			label := e.name
			if e.detail != "" {
				label += " (" + e.detail + ")"
			}
			out = append(out, label)
		}
	}
	return out
}

func composeToast(events []toastEvent) Toast {
	dropped, restored, failed := names(events, toastDropped), names(events, toastRestored), names(events, toastFailed)
	if len(events) == 1 {
		e := events[0]
		switch e.kind {
		case toastDropped:
			return Toast{Title: "Tunnel disconnected", Body: e.name + " lost its connection and is reconnecting."}
		case toastRestored:
			return Toast{Title: "Tunnel restored", Body: e.name + " is connected again."}
		default:
			return Toast{Title: "Tunnel stopped: " + e.detail, Body: e.name + " needs attention. Open the manager to fix it."}
		}
	}
	switch {
	case len(dropped) == len(events):
		return Toast{Title: fmt.Sprintf("%d tunnels disconnected", len(events)), Body: strings.Join(dropped, ", ") + " - reconnecting."}
	case len(restored) == len(events):
		return Toast{Title: fmt.Sprintf("%d tunnels restored", len(events)), Body: strings.Join(restored, ", ") + " connected again."}
	case len(failed) == len(events):
		return Toast{Title: fmt.Sprintf("%d tunnels stopped", len(events)), Body: strings.Join(failed, ", ")}
	}
	var lines []string
	for _, g := range []struct {
		label string
		items []string
	}{{"Disconnected", dropped}, {"Restored", restored}, {"Stopped", failed}} {
		if len(g.items) > 0 {
			lines = append(lines, g.label+": "+strings.Join(g.items, ", "))
		}
	}
	return Toast{Title: "SSH Tunnel Manager", Body: strings.Join(lines, "\n")}
}
