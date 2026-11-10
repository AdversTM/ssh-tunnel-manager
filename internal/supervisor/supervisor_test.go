package supervisor

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ssh-tunnel-manager/internal/backend"
	"ssh-tunnel-manager/internal/config"
)

type script struct {
	lines     []string
	exit      bool          // exit right after lines
	exitAfter time.Duration // or exit after this long (0 = wait for kill)
}

type fakeProc struct {
	lines  chan string
	done   chan struct{}
	killed atomic.Bool
	once   sync.Once
	pid    int
}

func (p *fakeProc) Lines() <-chan string  { return p.lines }
func (p *fakeProc) Done() <-chan struct{} { return p.done }
func (p *fakeProc) PID() int              { return p.pid }
func (p *fakeProc) Kill() error {
	p.killed.Store(true)
	p.finish()
	return nil
}
func (p *fakeProc) finish() {
	p.once.Do(func() {
		close(p.lines)
		close(p.done)
	})
}

type fake struct {
	mu       sync.Mutex
	scripts  []script
	procs    []*fakeProc
	delays   []int
	cleanups atomic.Int32
}

func (f *fake) start(string, []string, []string) (Process, error) {
	f.mu.Lock()
	sc := script{}
	if len(f.scripts) > 0 {
		sc, f.scripts = f.scripts[0], f.scripts[1:]
	}
	p := &fakeProc{lines: make(chan string, 64), done: make(chan struct{}), pid: 1000 + len(f.procs)}
	f.procs = append(f.procs, p)
	f.mu.Unlock()
	for _, l := range sc.lines {
		p.lines <- l
	}
	switch {
	case sc.exit:
		p.finish()
	case sc.exitAfter > 0:
		time.AfterFunc(sc.exitAfter, p.finish)
	}
	return p, nil
}

func (f *fake) starts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.procs)
}

func (f *fake) proc(i int) *fakeProc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.procs[i]
}

func newSup(f *fake, delay time.Duration, stable time.Duration) *Supervisor {
	return New(Options{
		Prepare: func(config.Tunnel) (backend.Options, func(), error) {
			return backend.Options{}, func() { f.cleanups.Add(1) }, nil
		},
		Start: f.start,
		Delay: func(n int) time.Duration {
			f.mu.Lock()
			f.delays = append(f.delays, n)
			f.mu.Unlock()
			return delay
		},
		StableAfter: stable,
	})
}

func tun(b config.Backend) config.Tunnel {
	return config.Tunnel{ID: "t1", Backend: b}
}

func waitState(t *testing.T, s *Supervisor, want State) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := s.Status("t1"); ok && st.State == want {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	st, _ := s.Status("t1")
	t.Fatalf("state %q not reached; last %+v", want, st)
	return Status{}
}

const (
	sshUp      = "debug1: remote forward success for: listen 127.0.0.1:18080, connect 127.0.0.1:18081"
	sshTimeout = "Timeout, server 127.0.0.1 not responding."
	sshDenied  = "pwuser@127.0.0.1: Permission denied (publickey,password)."
)

func TestBackoffDelay(t *testing.T) {
	mid := func() float64 { return 0.5 }
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := BackoffDelay(i+1, mid); got != w*time.Second {
			t.Errorf("attempt %d: got %v want %v", i+1, got, w*time.Second)
		}
	}
	if lo := BackoffDelay(7, func() float64 { return 0 }); lo != 48*time.Second {
		t.Errorf("low jitter %v", lo)
	}
	if hi := BackoffDelay(7, func() float64 { return 0.999999 }); hi < 71*time.Second || hi > 72*time.Second {
		t.Errorf("high jitter %v", hi)
	}
}

func TestNetworkFailuresRetryWithGrowingAttempts(t *testing.T) {
	f := &fake{scripts: []script{
		{lines: []string{sshTimeout}, exit: true},
		{lines: []string{sshTimeout}, exit: true},
		{lines: []string{sshTimeout}, exit: true},
		{lines: []string{sshUp}},
	}}
	s := newSup(f, time.Millisecond, time.Hour)
	s.Start(tun(config.BackendOpenSSH))
	st := waitState(t, s, Up)
	if st.Attempt != 3 {
		t.Fatalf("attempt=%d", st.Attempt)
	}
	f.mu.Lock()
	delays := append([]int(nil), f.delays...)
	f.mu.Unlock()
	if len(delays) != 3 || delays[0] != 1 || delays[1] != 2 || delays[2] != 3 {
		t.Fatalf("delays=%v", delays)
	}
	s.StopAll()
	if got := f.cleanups.Load(); got != 4 {
		t.Fatalf("cleanups=%d want 4", got)
	}
}

func TestAuthFailureGoesToErrorWithoutRetry(t *testing.T) {
	f := &fake{scripts: []script{{lines: []string{sshDenied}, exit: true}}}
	s := newSup(f, time.Millisecond, time.Hour)
	s.Start(tun(config.BackendOpenSSH))
	st := waitState(t, s, Error)
	if st.ErrorKind != "auth" || st.LastError != sshDenied {
		t.Fatalf("status=%+v", st)
	}
	time.Sleep(50 * time.Millisecond)
	if n := f.starts(); n != 1 {
		t.Fatalf("starts=%d, auth must not retry", n)
	}
}

func TestStableUpResetsAttempt(t *testing.T) {
	f := &fake{scripts: []script{
		{lines: []string{sshTimeout}, exit: true},
		{lines: []string{sshUp}, exitAfter: 150 * time.Millisecond},
		{lines: []string{sshUp}},
	}}
	s := newSup(f, time.Millisecond, 50*time.Millisecond)
	s.Start(tun(config.BackendOpenSSH))
	deadline := time.Now().Add(3 * time.Second)
	for f.starts() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	waitState(t, s, Up)
	f.mu.Lock()
	delays := append([]int(nil), f.delays...)
	f.mu.Unlock()
	if len(delays) != 2 || delays[0] != 1 || delays[1] != 1 {
		t.Fatalf("delays=%v, want [1 1] (reset after stable up)", delays)
	}
	s.StopAll()
}

func TestStopDuringBackoff(t *testing.T) {
	f := &fake{scripts: []script{{lines: []string{sshTimeout}, exit: true}}}
	s := newSup(f, time.Hour, time.Hour)
	s.Start(tun(config.BackendOpenSSH))
	st := waitState(t, s, Backoff)
	if st.NextRetry.IsZero() || st.ErrorKind != "network" {
		t.Fatalf("status=%+v", st)
	}
	s.Stop("t1")
	waitState(t, s, Stopped)
	if n := f.starts(); n != 1 {
		t.Fatalf("starts=%d", n)
	}
}

func TestStopWhileUpKillsProcess(t *testing.T) {
	f := &fake{scripts: []script{{lines: []string{sshUp}}}}
	s := newSup(f, time.Millisecond, time.Hour)
	s.Start(tun(config.BackendOpenSSH))
	waitState(t, s, Up)
	s.Stop("t1")
	if st, _ := s.Status("t1"); st.State != Stopped {
		t.Fatalf("state=%s", st.State)
	}
	if !f.proc(0).killed.Load() {
		t.Fatal("process not killed")
	}
}

func TestPlinkRefusedForwardKillsProcess(t *testing.T) {
	f := &fake{scripts: []script{{lines: []string{"Remote port forwarding from 127.0.0.1:18080 refused"}}}}
	s := newSup(f, time.Hour, time.Hour)
	s.Start(tun(config.BackendPlink))
	st := waitState(t, s, Backoff)
	if st.ErrorKind != "remote-port-busy" {
		t.Fatalf("status=%+v", st)
	}
	if !f.proc(0).killed.Load() {
		t.Fatal("plink must be killed on refused forward")
	}
	s.StopAll()
}

func TestRestartReconnectsImmediately(t *testing.T) {
	f := &fake{scripts: []script{{lines: []string{sshUp}}, {lines: []string{sshUp}}}}
	s := newSup(f, time.Hour, time.Hour)
	s.Start(tun(config.BackendOpenSSH))
	waitState(t, s, Up)
	s.Restart("t1")
	deadline := time.Now().Add(3 * time.Second)
	for f.starts() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.starts() != 2 || !f.proc(0).killed.Load() {
		t.Fatalf("starts=%d killed=%v", f.starts(), f.proc(0).killed.Load())
	}
	st := waitState(t, s, Up)
	if st.Attempt != 0 || st.PID != 1001 {
		t.Fatalf("status=%+v", st)
	}
	s.StopAll()
}

func TestHostKeyUnknownRecordsFingerprint(t *testing.T) {
	f := &fake{scripts: []script{{lines: []string{
		"ssh-ed25519 255 SHA256:EBzbLTwm81OXNzrr0rX1rLmnWRz3ba8v9K4hyJjmA1Y",
		"FATAL ERROR: Cannot confirm a host key in batch mode",
	}, exit: true}}}
	s := newSup(f, time.Millisecond, time.Hour)
	s.Start(tun(config.BackendPlink))
	st := waitState(t, s, Error)
	if st.ErrorKind != "host-key-unknown" || st.Fingerprint != "SHA256:EBzbLTwm81OXNzrr0rX1rLmnWRz3ba8v9K4hyJjmA1Y" {
		t.Fatalf("status=%+v", st)
	}
}

func TestCleanupRunsOnceAfterAuthentication(t *testing.T) {
	f := &fake{scripts: []script{{lines: []string{"Access granted", "Remote port forwarding from 127.0.0.1:18080 enabled"}}}}
	s := newSup(f, time.Millisecond, time.Hour)
	s.Start(tun(config.BackendPlink))
	waitState(t, s, Up)
	if got := f.cleanups.Load(); got != 1 {
		t.Fatalf("cleanups while up=%d, want 1 (early cleanup after auth)", got)
	}
	s.StopAll()
	if got := f.cleanups.Load(); got != 1 {
		t.Fatalf("cleanups after stop=%d, want still 1", got)
	}
}

func TestStartIsIdempotentAndRestartsAfterError(t *testing.T) {
	f := &fake{scripts: []script{{lines: []string{sshDenied}, exit: true}, {lines: []string{sshUp}}}}
	s := newSup(f, time.Millisecond, time.Hour)
	s.Start(tun(config.BackendOpenSSH))
	waitState(t, s, Error)
	s.Start(tun(config.BackendOpenSSH))
	waitState(t, s, Up)
	s.Start(tun(config.BackendOpenSSH))
	time.Sleep(30 * time.Millisecond)
	if n := f.starts(); n != 2 {
		t.Fatalf("starts=%d", n)
	}
	s.StopAll()
}
