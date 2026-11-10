package manager

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/supervisor"
)

// seqStart plays scripts[host][n] on the n-th start of that host (the last one repeats).
func seqStart(scripts map[string][][]string, starts *int, mu *sync.Mutex) supervisor.StartFunc {
	calls := map[string]int{}
	return func(exe string, args, env []string) (supervisor.Process, error) {
		host := args[len(args)-1]
		mu.Lock()
		seq := scripts[host]
		n := min(calls[host], len(seq)-1)
		calls[host]++
		*starts++
		mu.Unlock()
		return fakeStart(map[string][]string{host: seq[n]}, new([][]string), new(sync.Mutex))(exe, args, env)
	}
}

var plinkDrop = append(slices.Clone(plinkUp), "Network error: Software caused connection abort", "EXIT")

type toastEnv struct {
	m      *Manager
	mu     sync.Mutex
	toasts []Toast
	starts int
}

func newToastEnv(t *testing.T, scripts map[string][][]string, backoff, window time.Duration, autostart func(bool) error) *toastEnv {
	t.Helper()
	e := &toastEnv{}
	m, err := New(Options{
		DataDir: t.TempDir(), Secrets: &memStore{m: map[string]string{}},
		Start: seqStart(scripts, &e.starts, &e.mu), Delay: func(int) time.Duration { return backoff },
		Toast:      func(x Toast) { e.mu.Lock(); e.toasts = append(e.toasts, x); e.mu.Unlock() },
		ToastDelay: window, Autostart: autostart,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	e.m = m
	return e
}

func (e *toastEnv) add(t *testing.T, host string) config.Tunnel {
	t.Helper()
	tn, err := e.m.SaveTunnel(plinkTunnel(host), nil)
	if err != nil {
		t.Fatal(err)
	}
	return tn
}

func (e *toastEnv) got() []Toast {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.toasts)
}

func (e *toastEnv) waitToasts(t *testing.T, n int) []Toast {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(e.got()) < n {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if got := e.got(); len(got) != n {
		t.Fatalf("toasts %+v, want %d", got, n)
	}
	return e.got()
}

func TestToastsDropAndRestore(t *testing.T) {
	e := newToastEnv(t, map[string][][]string{"h": {plinkDrop, plinkUp}}, 150*time.Millisecond, 30*time.Millisecond, nil)
	tn := e.add(t, "h")
	if err := e.m.Connect(tn.ID); err != nil {
		t.Fatal(err)
	}
	got := e.waitToasts(t, 2)
	if got[0].Title != "Tunnel disconnected" || !strings.Contains(got[0].Body, "dev-h") ||
		got[1].Title != "Tunnel restored" {
		t.Fatalf("toasts %+v", got)
	}
	if e.m.status(tn.ID).State != supervisor.Up {
		t.Fatal("tunnel should be up again")
	}
}

func TestToastsAggregateSimultaneousDrops(t *testing.T) {
	scripts := map[string][][]string{}
	e := newToastEnv(t, scripts, 400*time.Millisecond, 150*time.Millisecond, nil)
	var ids []string
	for _, h := range []string{"a", "b", "c"} {
		scripts[h] = [][]string{plinkDrop, plinkUp}
		ids = append(ids, e.add(t, h).ID)
	}
	for _, id := range ids {
		if err := e.m.Connect(id); err != nil {
			t.Fatal(err)
		}
	}
	got := e.waitToasts(t, 2)
	if got[0].Title != "3 tunnels disconnected" || got[1].Title != "3 tunnels restored" {
		t.Fatalf("toasts %+v", got)
	}
	for _, n := range []string{"dev-a", "dev-b", "dev-c"} {
		if !strings.Contains(got[0].Body, n) {
			t.Fatalf("aggregated body %q misses %s", got[0].Body, n)
		}
	}
}

func TestToastsOffSendsNothing(t *testing.T) {
	e := newToastEnv(t, map[string][][]string{"h": {plinkDrop, plinkUp}}, 50*time.Millisecond, 20*time.Millisecond, nil)
	if err := e.m.SaveSettings(config.Settings{Notifications: false}); err != nil {
		t.Fatal(err)
	}
	tn := e.add(t, "h")
	if err := e.m.Connect(tn.ID); err != nil {
		t.Fatal(err)
	}
	e.waitToasts(t, 0)
}

func TestToastOnErrorAndNoneForBlip(t *testing.T) {
	e := newToastEnv(t, map[string][][]string{
		"bad":  {{"Configured password was not accepted", "EXIT"}},
		"blip": {plinkDrop, plinkUp},
	}, time.Millisecond, 300*time.Millisecond, nil)
	bad, blip := e.add(t, "bad"), e.add(t, "blip")
	if err := e.m.Connect(blip.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Connect(bad.ID); err != nil {
		t.Fatal(err)
	}
	got := e.waitToasts(t, 1)
	if got[0].Title != "Tunnel stopped: authentication failed" || !strings.Contains(got[0].Body, "dev-bad") {
		t.Fatalf("toasts %+v", got)
	}
}

func TestRestartActiveAfterResume(t *testing.T) {
	e := newToastEnv(t, map[string][][]string{"up": {plinkUp}, "idle": {plinkUp}}, time.Hour, 20*time.Millisecond, nil)
	up, idle := e.add(t, "up"), e.add(t, "idle")
	if err := e.m.Connect(up.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.m, up.ID, supervisor.Up)
	e.m.RestartActive("resume")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		n := e.starts
		e.mu.Unlock()
		if n == 2 && e.m.status(up.ID).State == supervisor.Up {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e.starts != 2 {
		t.Fatalf("starts=%d want 2 (restart of the active tunnel only)", e.starts)
	}
	var texts []string
	for _, l := range e.m.Logs(up.ID, false) {
		texts = append(texts, l.Text)
	}
	if !slices.Contains(texts, "resume → restart") {
		t.Fatalf("log %q", texts)
	}
	if len(e.m.Logs(idle.ID, false)) != 0 {
		t.Fatal("stopped tunnel must not be restarted")
	}
	e.waitToasts(t, 0)
}

func TestSaveSettingsAppliesAutostart(t *testing.T) {
	var calls []bool
	fail := false
	e := newToastEnv(t, nil, time.Hour, time.Hour, func(on bool) error {
		calls = append(calls, on)
		if fail {
			return errors.New("registry denied")
		}
		return nil
	})
	notif := config.Settings{Notifications: true}
	if err := e.m.SaveSettings(notif); err != nil || len(calls) != 0 {
		t.Fatalf("off→off must not touch autostart: %v %v", calls, err)
	}
	on := notif
	on.Autostart = true
	if err := e.m.SaveSettings(on); err != nil {
		t.Fatal(err)
	}
	if err := e.m.SaveSettings(notif); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []bool{true, false}) {
		t.Fatalf("calls %v", calls)
	}
	fail = true
	if err := e.m.SaveSettings(on); err == nil || e.m.Settings().Autostart {
		t.Fatal("failed autostart must reject the settings change")
	}
}
