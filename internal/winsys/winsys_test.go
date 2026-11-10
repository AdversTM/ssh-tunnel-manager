package winsys

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCommandQuoting(t *testing.T) {
	got := Command(`C:\Program Files\STM\ssh-tunnel-manager.exe`, "-data", `C:\Users\a b\data`)
	want := `"C:\Program Files\STM\ssh-tunnel-manager.exe" -data "C:\Users\a b\data"`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestAutostartRoundTrip(t *testing.T) {
	name := "SshTunnelManager-unittest"
	t.Cleanup(func() {
		if err := RemoveAutostart(name); err != nil {
			t.Error(err)
		}
	})
	if v, err := Autostart(name); err != nil || v != "" {
		t.Fatalf("before: %q %v", v, err)
	}
	cmd := Command(`C:\x\ssh-tunnel-manager.exe`)
	if err := SetAutostart(name, cmd); err != nil {
		t.Fatal(err)
	}
	if v, err := Autostart(name); err != nil || v != cmd {
		t.Fatalf("after set: %q %v", v, err)
	}
	if err := RemoveAutostart(name); err != nil {
		t.Fatal(err)
	}
	if v, err := Autostart(name); err != nil || v != "" {
		t.Fatalf("after remove: %q %v", v, err)
	}
	if err := RemoveAutostart(name); err != nil {
		t.Fatalf("removing twice must be fine: %v", err)
	}
}

func TestResumeFilterCollapsesWakeEvents(t *testing.T) {
	var n atomic.Int32
	done := make(chan struct{}, 4)
	f := &resumeFilter{window: 30 * time.Second, onResume: func() { n.Add(1); done <- struct{}{} }}
	t0 := time.Now()
	f.event(0x4, t0) // PBT_APMSUSPEND: ignored
	f.event(pbtAPMResumeAutomatic, t0)
	f.event(pbtAPMResumeSuspend, t0.Add(2*time.Second))
	<-done
	f.event(pbtAPMResumeAutomatic, t0.Add(time.Minute))
	<-done
	time.Sleep(20 * time.Millisecond)
	if n.Load() != 2 {
		t.Fatalf("resume calls=%d want 2", n.Load())
	}
}

func TestPowerDispatchRoutesToWatcher(t *testing.T) {
	called := make(chan struct{}, 1)
	stop, err := WatchResume(func() { called <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	powerMu.Lock()
	var id uintptr
	for k := range powerFilters {
		id = k
	}
	powerMu.Unlock()
	powerDispatch(id, pbtAPMResumeAutomatic, 0)
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("simulated resume not delivered")
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	powerMu.Lock()
	left := len(powerFilters)
	powerMu.Unlock()
	if left != 0 {
		t.Fatalf("filters left: %d", left)
	}
}

func TestNetworkFingerprintThisMachine(t *testing.T) {
	fp, err := NetworkFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fp == "" || !strings.Contains(fp, "gw=") {
		t.Fatalf("expected at least one adapter with a gateway, got %q", fp)
	}
}

func TestNetWatcherDebouncesAndReportsChanges(t *testing.T) {
	var mu sync.Mutex
	current := "eth=10.0.0.2,gw=10.0.0.1"
	var got []bool
	changed := make(chan struct{}, 8)
	w := &netWatcher{
		debounce: 30 * time.Millisecond, last: current,
		fingerprint: func() (string, error) { mu.Lock(); defer mu.Unlock(); return current, nil },
		onChange:    func(online bool) { mu.Lock(); got = append(got, online); mu.Unlock(); changed <- struct{}{} },
		onError:     func(err error) { t.Error(err) },
	}
	set := func(s string) { mu.Lock(); current = s; mu.Unlock() }

	for range 5 {
		w.trigger()
	}
	time.Sleep(80 * time.Millisecond)
	if len(changed) != 0 {
		t.Fatal("unchanged fingerprint must not report")
	}
	set("")
	w.trigger()
	w.trigger()
	<-changed
	set("wifi=192.168.1.5,gw=192.168.1.1")
	w.trigger()
	<-changed
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] || !got[1] {
		t.Fatalf("changes %v, want [false true]", got)
	}
}

func TestNetWatcherReportsErrors(t *testing.T) {
	errc := make(chan error, 1)
	w := &netWatcher{debounce: time.Millisecond, fingerprint: func() (string, error) { return "", errors.New("boom") },
		onChange: func(bool) { t.Error("no change expected") }, onError: func(err error) { errc <- err }}
	w.trigger()
	if err := <-errc; err.Error() != "boom" {
		t.Fatal(err)
	}
}

func TestWatchNetworkRegisters(t *testing.T) {
	stop, err := WatchNetwork(time.Second, func(bool) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}
