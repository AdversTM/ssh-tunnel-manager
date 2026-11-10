package supervisor

import (
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"ssh-tunnel-manager/internal/backend"
	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/logs"
	"ssh-tunnel-manager/internal/proc"
)

type State string

const (
	Stopped    State = "stopped"
	Connecting State = "connecting"
	Up         State = "up"
	Backoff    State = "backoff"
	Error      State = "error"
)

type Status struct {
	ID          string    `json:"id"`
	State       State     `json:"state"`
	Attempt     int       `json:"attempt"`
	ErrorKind   string    `json:"errorKind,omitempty"`
	LastError   string    `json:"lastError,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	PID         int       `json:"pid,omitempty"`
	Since       time.Time `json:"since"`
	NextRetry   time.Time `json:"nextRetry,omitempty"`
}

type Process interface {
	Lines() <-chan string
	Done() <-chan struct{}
	Kill() error
	PID() int
}

type StartFunc func(exe string, args, env []string) (Process, error)

// PrepareFunc builds per-attempt backend options (askpass env, password file…)
// and returns a cleanup run after the attempt ends.
type PrepareFunc func(t config.Tunnel) (backend.Options, func(), error)

type Options struct {
	Prepare     PrepareFunc
	Start       StartFunc
	Delay       func(attempt int) time.Duration
	StableAfter time.Duration
	OnStatus    func(Status)
	OnLine      func(id string, l logs.Line)
}

var backoffSteps = []time.Duration{1, 2, 4, 8, 16, 32, 60}

// BackoffDelay returns the wait before reconnect attempt n (1-based):
// 1,2,4,…,60 s capped, with ±20% jitter from rnd in [0,1).
func BackoffDelay(attempt int, rnd func() float64) time.Duration {
	i := min(max(attempt-1, 0), len(backoffSteps)-1)
	base := backoffSteps[i] * time.Second
	return time.Duration(float64(base) * (0.8 + 0.4*rnd()))
}

func startProc(exe string, args, env []string) (Process, error) {
	p, err := proc.Start(exe, args, env)
	if err != nil {
		return nil, err
	}
	return p, nil
}

type runner struct {
	t        config.Tunnel
	stop     chan struct{}
	stopOnce sync.Once
	restart  chan struct{}
	done     chan struct{}
}

type Supervisor struct {
	o        Options
	mu       sync.Mutex
	runners  map[string]*runner
	statuses map[string]Status
}

func New(o Options) *Supervisor {
	if o.Start == nil {
		o.Start = startProc
	}
	if o.Delay == nil {
		o.Delay = func(n int) time.Duration { return BackoffDelay(n, rand.Float64) }
	}
	if o.StableAfter == 0 {
		o.StableAfter = 60 * time.Second
	}
	if o.OnStatus == nil {
		o.OnStatus = func(Status) {}
	}
	if o.OnLine == nil {
		o.OnLine = func(string, logs.Line) {}
	}
	return &Supervisor{o: o, runners: map[string]*runner{}, statuses: map[string]Status{}}
}

func isDone(r *runner) bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// Start connects the tunnel; no-op if it is already running.
func (s *Supervisor) Start(t config.Tunnel) {
	s.mu.Lock()
	if r, ok := s.runners[t.ID]; ok && !isDone(r) {
		s.mu.Unlock()
		return
	}
	r := &runner{t: t, stop: make(chan struct{}), restart: make(chan struct{}, 1), done: make(chan struct{})}
	s.runners[t.ID] = r
	s.mu.Unlock()
	go s.run(r)
}

// Stop disconnects the tunnel and waits until its process is gone.
func (s *Supervisor) Stop(id string) {
	s.mu.Lock()
	r := s.runners[id]
	s.mu.Unlock()
	if r == nil {
		return
	}
	r.stopOnce.Do(func() { close(r.stop) })
	<-r.done
	if st, _ := s.Status(id); st.State != Stopped {
		s.update(id, func(st *Status) { *st = Status{ID: id, State: Stopped} })
	}
}

func (s *Supervisor) StopAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.runners))
	for id := range s.runners {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() { s.Stop(id) })
	}
	wg.Wait()
}

// Remove stops the tunnel and forgets its status.
func (s *Supervisor) Remove(id string) {
	s.Stop(id)
	s.mu.Lock()
	delete(s.runners, id)
	delete(s.statuses, id)
	s.mu.Unlock()
}

// Restart reconnects immediately with backoff reset (e.g. after resume).
func (s *Supervisor) Restart(id string) {
	s.mu.Lock()
	r := s.runners[id]
	s.mu.Unlock()
	if r == nil || isDone(r) {
		return
	}
	select {
	case r.restart <- struct{}{}:
	default:
	}
}

func (s *Supervisor) RestartActive() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.runners))
	for id, r := range s.runners {
		if !isDone(r) {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.Restart(id)
	}
}

func (s *Supervisor) Status(id string) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.statuses[id]
	return st, ok
}

func (s *Supervisor) Statuses() []Status {
	s.mu.Lock()
	out := make([]Status, 0, len(s.statuses))
	for _, st := range s.statuses {
		out = append(out, st)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Supervisor) update(id string, f func(*Status)) {
	s.mu.Lock()
	st := s.statuses[id]
	prev := st.State
	f(&st)
	st.ID = id
	if st.State != prev || st.Since.IsZero() {
		st.Since = time.Now()
	}
	s.statuses[id] = st
	s.mu.Unlock()
	s.o.OnStatus(st)
}

type outcome int

const (
	exited outcome = iota
	stopped
	restarted
)

type attemptResult struct {
	kind        backend.Kind
	detail      string
	fingerprint string
	outcome     outcome
}

func (s *Supervisor) run(r *runner) {
	defer close(r.done)
	id := r.t.ID
	attempt := 0
	for {
		s.update(id, func(st *Status) {
			*st = Status{State: Connecting, Attempt: attempt, LastError: st.LastError, ErrorKind: st.ErrorKind}
		})
		res := s.attempt(r, &attempt)
		switch res.outcome {
		case stopped:
			s.update(id, func(st *Status) { *st = Status{State: Stopped} })
			return
		case restarted:
			attempt = 0
			continue
		}
		if !res.kind.Retryable() {
			s.update(id, func(st *Status) {
				*st = Status{State: Error, Attempt: attempt, ErrorKind: res.kind.String(), LastError: res.detail, Fingerprint: res.fingerprint}
			})
			return
		}
		attempt++
		d := s.o.Delay(attempt)
		s.update(id, func(st *Status) {
			*st = Status{State: Backoff, Attempt: attempt, ErrorKind: res.kind.String(), LastError: res.detail,
				Fingerprint: res.fingerprint, NextRetry: time.Now().Add(d)}
		})
		timer := time.NewTimer(d)
		select {
		case <-r.stop:
			timer.Stop()
			s.update(id, func(st *Status) { *st = Status{State: Stopped} })
			return
		case <-r.restart:
			timer.Stop()
			attempt = 0
		case <-timer.C:
		}
	}
}

func drain(p Process) {
	for range p.Lines() {
	}
	<-p.Done()
}

func (s *Supervisor) attempt(r *runner, attempt *int) attemptResult {
	id := r.t.ID
	be := backend.For(r.t.Backend)
	o, prepCleanup, err := s.o.Prepare(r.t)
	if err != nil {
		return attemptResult{kind: backend.KindConfig, detail: err.Error()}
	}
	// Runs early once the client has authenticated (e.g. deletes the plink
	// password file), or when the attempt ends.
	cleanup := sync.OnceFunc(prepCleanup)
	defer cleanup()
	cmd := be.Command(r.t, o)
	p, err := s.o.Start(cmd.Exe, cmd.Args, cmd.Env)
	if err != nil {
		return attemptResult{kind: backend.KindConfig, detail: err.Error()}
	}
	s.update(id, func(st *Status) { st.PID = p.PID() })

	res := attemptResult{outcome: exited}
	var stable <-chan time.Time
	lines := p.Lines()
	for lines != nil {
		select {
		case l, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			info := be.ClassifyLine(l)
			s.o.OnLine(id, logs.Line{Time: time.Now(), Text: l, Debug: info.Debug})
			if info.Fingerprint != "" {
				res.fingerprint = info.Fingerprint
			}
			if info.Authenticated {
				cleanup()
			}
			if info.Up && stable == nil {
				stable = time.After(s.o.StableAfter)
				s.update(id, func(st *Status) {
					*st = Status{State: Up, Attempt: *attempt, PID: p.PID(), Fingerprint: res.fingerprint}
				})
			}
			if info.Failure > res.kind {
				res.kind, res.detail = info.Failure, l
				if info.Failure.KillsProcess() {
					if err := p.Kill(); err != nil {
						s.o.OnLine(id, logs.Line{Time: time.Now(), Text: "kill failed: " + err.Error()})
					}
				}
			}
		case <-stable:
			*attempt = 0
			stable = nil
		case <-r.stop:
			s.kill(id, p)
			return attemptResult{outcome: stopped}
		case <-r.restart:
			s.kill(id, p)
			return attemptResult{outcome: restarted}
		}
	}
	<-p.Done()
	if res.kind == backend.KindNone {
		res.detail = "connection closed"
	}
	return res
}

func (s *Supervisor) kill(id string, p Process) {
	if err := p.Kill(); err != nil {
		s.o.OnLine(id, logs.Line{Time: time.Now(), Text: "kill failed: " + err.Error()})
	}
	drain(p)
}
