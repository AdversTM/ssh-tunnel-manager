package manager

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/engine"
	"ssh-tunnel-manager/internal/secrets"
	"ssh-tunnel-manager/internal/supervisor"
)

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) Get(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[ref]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (s *memStore) Set(ref, v string) error { s.mu.Lock(); s.m[ref] = v; s.mu.Unlock(); return nil }
func (s *memStore) Delete(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[ref]; !ok {
		return secrets.ErrNotFound
	}
	delete(s.m, ref)
	return nil
}

type fakeProc struct {
	lines chan string
	done  chan struct{}
	kill  chan struct{}
	once  sync.Once
}

func (p *fakeProc) Lines() <-chan string  { return p.lines }
func (p *fakeProc) Done() <-chan struct{} { return p.done }
func (p *fakeProc) PID() int              { return 4242 }
func (p *fakeProc) Kill() error           { p.once.Do(func() { close(p.kill) }); return nil }

// fakeStart emits script lines for the tunnel host (last arg), then either
// exits ("EXIT" line) or waits to be killed.
func fakeStart(scripts map[string][]string, argv *[][]string, mu *sync.Mutex) supervisor.StartFunc {
	return func(exe string, args, env []string) (supervisor.Process, error) {
		mu.Lock()
		*argv = append(*argv, args)
		mu.Unlock()
		p := &fakeProc{lines: make(chan string), done: make(chan struct{}), kill: make(chan struct{})}
		go func() {
			defer close(p.done)
			defer close(p.lines)
			for _, l := range scripts[args[len(args)-1]] {
				if l == "EXIT" {
					return
				}
				select {
				case p.lines <- l:
				case <-p.kill:
					return
				}
			}
			<-p.kill
		}()
		return p, nil
	}
}

var plinkUp = []string{"Access granted", "Using SSH protocol version 2", "Remote port forwarding from 127.0.0.1:18080 enabled"}

type env struct {
	m       *Manager
	store   *memStore
	dir     string
	mu      sync.Mutex
	argv    [][]string
	changes int
}

func newEnv(t *testing.T, scripts map[string][]string) *env {
	t.Helper()
	e := &env{store: &memStore{m: map[string]string{}}, dir: t.TempDir()}
	m, err := New(Options{
		DataDir: e.dir, HelperExe: `C:\app.exe`, Secrets: e.store,
		Start: fakeStart(scripts, &e.argv, &e.mu), Delay: func(int) time.Duration { return time.Hour },
		HostKeyTimeout: time.Hour,
		OnChange:       func() { e.mu.Lock(); e.changes++; e.mu.Unlock() },
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

func plinkTunnel(host string) config.Tunnel {
	return config.Tunnel{Name: "dev-" + host, Backend: config.BackendPlink, Host: host, Port: 2222, User: "u",
		Auth:    config.Auth{Method: config.AuthAgent},
		Forward: config.Forward{Type: config.ForwardRemote, BindAddress: "127.0.0.1", BindPort: 18080, TargetHost: "127.0.0.1", TargetPort: 3000}}
}

func waitState(t *testing.T, m *Manager, id string, want supervisor.State) supervisor.Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := m.status(id); st.State == want {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state %s never reached; last %+v", want, m.status(id))
	return supervisor.Status{}
}

func waitRequests(t *testing.T, m *Manager, n int) []HostKeyRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r := m.HostKeyRequests(); len(r) == n {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("want %d host key requests, have %+v", n, m.HostKeyRequests())
	return nil
}

func ptr(s string) *string { return &s }

func TestSaveTunnelValidatesAndPersists(t *testing.T) {
	e := newEnv(t, nil)
	bad := plinkTunnel("h")
	bad.Forward.BindPort = 0
	if _, err := e.m.SaveTunnel(bad, nil); err == nil || !strings.Contains(err.Error(), "bind port") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "config.json")); !os.IsNotExist(err) {
		t.Fatal("invalid tunnel must not be saved")
	}
	saved, err := e.m.SaveTunnel(plinkTunnel("h"), ptr("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.Auth.Agent != config.AgentPageant || saved.Server.Mode != config.ServerNone {
		t.Fatalf("saved %+v", saved)
	}
	cfg, err := config.Load(filepath.Join(e.dir, "config.json"))
	if err != nil || len(cfg.Tunnels) != 1 || cfg.Tunnels[0].ID != saved.ID {
		t.Fatalf("config %+v err %v", cfg, err)
	}
	data, err := os.ReadFile(filepath.Join(e.dir, "config.json"))
	if err != nil || strings.Contains(string(data), `"pw"`) {
		t.Fatalf("secret must not be in config: %s", data)
	}
	v := e.m.Tunnels()
	if len(v) != 1 || !v[0].HasSecret || v[0].Status.State != supervisor.Stopped {
		t.Fatalf("views %+v", v)
	}
	if got, _ := e.store.Get("ssh-tunnel-manager:" + saved.ID); got != "pw" {
		t.Fatalf("stored secret %q", got)
	}
}

func TestEditKeepsOrResetsHostKey(t *testing.T) {
	e := newEnv(t, nil)
	tn, err := e.m.SaveTunnel(plinkTunnel("h"), nil)
	if err != nil {
		t.Fatal(err)
	}
	tn.HostKey.PlinkFingerprint = "SHA256:old"
	if err := e.m.replaceTunnel(tn); err != nil {
		t.Fatal(err)
	}
	tn.Name = "renamed"
	tn.HostKey = config.HostKey{}
	got, err := e.m.SaveTunnel(tn, nil)
	if err != nil || got.HostKey.PlinkFingerprint != "SHA256:old" || got.Name != "renamed" {
		t.Fatalf("rename: %+v %v", got, err)
	}
	got.Host = "other"
	got, err = e.m.SaveTunnel(got, ptr(""))
	if err != nil || got.HostKey.PlinkFingerprint != "" {
		t.Fatalf("host change must forget the key: %+v %v", got, err)
	}
}

func TestDeleteRemovesConfigAndSecret(t *testing.T) {
	e := newEnv(t, map[string][]string{"h": plinkUp})
	tn, err := e.m.SaveTunnel(plinkTunnel("h"), ptr("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.Connect(tn.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.m, tn.ID, supervisor.Up)
	if err := e.m.DeleteTunnel(tn.ID); err != nil {
		t.Fatal(err)
	}
	if len(e.m.Tunnels()) != 0 {
		t.Fatal("tunnel still listed")
	}
	if _, err := e.store.Get("ssh-tunnel-manager:" + tn.ID); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("secret still stored: %v", err)
	}
	if _, ok := e.m.sup.Status(tn.ID); ok {
		t.Fatal("supervisor still tracks deleted tunnel")
	}
}

func TestConnectLogsAndDisconnect(t *testing.T) {
	e := newEnv(t, map[string][]string{"h": plinkUp})
	tn, err := e.m.SaveTunnel(plinkTunnel("h"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.Connect(tn.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.m, tn.ID, supervisor.Up)
	texts := func(verbose bool) []string {
		var out []string
		for _, l := range e.m.Logs(tn.ID, verbose) {
			out = append(out, l.Text)
		}
		return out
	}
	quiet := texts(false)
	for _, want := range []string{"connecting", "Access granted", "Remote port forwarding from 127.0.0.1:18080 enabled", "connected (pid 4242)"} {
		if !slices.Contains(quiet, want) {
			t.Fatalf("missing %q in %q", want, quiet)
		}
	}
	if slices.Contains(quiet, "Using SSH protocol version 2") || !slices.Contains(texts(true), "Using SSH protocol version 2") {
		t.Fatal("debug line must be hidden unless verbose")
	}
	e.m.Disconnect(tn.ID)
	if st := e.m.status(tn.ID); st.State != supervisor.Stopped {
		t.Fatalf("after disconnect %+v", st)
	}
	if !slices.Contains(texts(false), "stopped") {
		t.Fatal("no stopped line")
	}
	logFile, err := os.ReadFile(filepath.Join(e.dir, "logs", "ssh-tunnel-manager.log"))
	if err != nil || !strings.Contains(string(logFile), "[dev-h] connected (pid 4242)") {
		t.Fatalf("log file %q err %v", logFile, err)
	}
}

func TestReconnectAttemptLogged(t *testing.T) {
	var mu sync.Mutex
	var argv [][]string
	m2, err := New(Options{DataDir: t.TempDir(), Secrets: &memStore{m: map[string]string{}},
		Start: fakeStart(map[string][]string{"h": {"Network error: Connection refused", "EXIT"}}, &argv, &mu),
		Delay: func(int) time.Duration { return time.Millisecond }})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	tn, err := m2.SaveTunnel(plinkTunnel("h"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m2.Connect(tn.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range m2.Logs(tn.ID, false) {
			if l.Text == "reconnect attempt 1" {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no reconnect line: %+v", m2.Logs(tn.ID, false))
}

func TestPlinkUnknownHostKeyAccept(t *testing.T) {
	scripts := map[string][]string{"h": {"ssh-ed25519 255 SHA256:newkey", "Cannot confirm a host key in batch mode", "EXIT"}}
	e := newEnv(t, scripts)
	tn, err := e.m.SaveTunnel(plinkTunnel("h"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.Connect(tn.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.m, tn.ID, supervisor.Error)
	reqs := waitRequests(t, e.m, 1)
	if r := reqs[0]; r.Changed || r.Fingerprint != "SHA256:newkey" || r.Host != "[h]:2222" || r.TunnelName != "dev-h" {
		t.Fatalf("request %+v", r)
	}
	scripts["h"] = plinkUp
	if err := e.m.AnswerHostKey(reqs[0].ID, true); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.m, tn.ID, supervisor.Up)
	cfg, err := config.Load(filepath.Join(e.dir, "config.json"))
	if err != nil || cfg.Tunnels[0].HostKey.PlinkFingerprint != "SHA256:newkey" {
		t.Fatalf("fingerprint not saved: %+v %v", cfg.Tunnels, err)
	}
	e.mu.Lock()
	last := e.argv[len(e.argv)-1]
	e.mu.Unlock()
	if i := slices.Index(last, "-hostkey"); i < 0 || last[i+1] != "SHA256:newkey" {
		t.Fatalf("reconnect argv %q", last)
	}
	if err := e.m.AnswerHostKey(reqs[0].ID, true); err == nil {
		t.Fatal("answering twice must fail")
	}
}

func TestPlinkChangedHostKeyRejectThenReview(t *testing.T) {
	scripts := map[string][]string{"h": {"ssh-ed25519 255 SHA256:evil", "WARNING - POTENTIAL SECURITY BREACH!", "EXIT"}}
	e := newEnv(t, scripts)
	tn := plinkTunnel("h")
	tn, err := e.m.SaveTunnel(tn, nil)
	if err != nil {
		t.Fatal(err)
	}
	tn.HostKey.PlinkFingerprint = "SHA256:good"
	if err := e.m.replaceTunnel(tn); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Connect(tn.ID); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, e.m, tn.ID, supervisor.Error)
	if st.ErrorKind != "host-key-changed" {
		t.Fatalf("status %+v", st)
	}
	r := waitRequests(t, e.m, 1)[0]
	if !r.Changed || r.OldFingerprint != "SHA256:good" || r.Fingerprint != "SHA256:evil" {
		t.Fatalf("request %+v", r)
	}
	if err := e.m.AnswerHostKey(r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitRequests(t, e.m, 0)
	if got, _ := e.m.tunnel(tn.ID); got.HostKey.PlinkFingerprint != "SHA256:good" {
		t.Fatal("rejecting must keep the stored key")
	}
	if e.m.status(tn.ID).State != supervisor.Error {
		t.Fatal("rejected tunnel must stay in error")
	}
	if err := e.m.ReviewHostKey(tn.ID); err != nil {
		t.Fatal(err)
	}
	waitRequests(t, e.m, 1)
	if err := e.m.ReviewHostKey(tn.ID); err == nil {
		t.Fatal("duplicate request must not be raised")
	}
}

func TestOpenSSHLivePrompt(t *testing.T) {
	e := newEnv(t, nil)
	e.m.o.HostKeyTimeout = 50 * time.Millisecond
	ask := func() chan bool {
		ch := make(chan bool, 1)
		go func() {
			ch <- e.m.hostKeyPrompt(engine.HostKeyRequest{TunnelID: "x", TunnelName: "dev", Host: "[h]:2222", KeyType: "ED25519", Fingerprint: "SHA256:k"})
		}()
		return ch
	}
	if got := <-ask(); got {
		t.Fatal("timeout must reject")
	}
	if len(e.m.HostKeyRequests()) != 0 {
		t.Fatal("timed out request still pending")
	}

	e.m.o.HostKeyTimeout = time.Hour
	ch := ask()
	r := waitRequests(t, e.m, 1)[0]
	if r.KeyType != "ED25519" || r.Backend != config.BackendOpenSSH || r.Changed {
		t.Fatalf("request %+v", r)
	}
	if err := e.m.AnswerHostKey(r.ID, true); err != nil || !<-ch {
		t.Fatalf("accept: %v", err)
	}

	ch = ask()
	waitRequests(t, e.m, 1)
	e.m.Disconnect("x")
	if <-ch {
		t.Fatal("disconnect must reject a pending prompt")
	}

	e.m.mu.Lock()
	e.m.trusted["x"] = "SHA256:k"
	e.m.mu.Unlock()
	if !<-ask() || len(e.m.HostKeyRequests()) != 0 {
		t.Fatal("key confirmed in the changed-key dialog must be accepted without asking again")
	}
}

func TestCorruptConfigStillStarts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{DataDir: dir, Secrets: &memStore{m: map[string]string{}}})
	if !errors.Is(err, config.ErrCorrupt) || m == nil {
		t.Fatalf("m=%v err=%v", m, err)
	}
	defer m.Close()
	if len(m.Tunnels()) != 0 {
		t.Fatal("expected empty config")
	}
	if b, _ := filepath.Glob(filepath.Join(dir, "config.json.bak-*")); len(b) != 1 {
		t.Fatalf("backup files %v", b)
	}
}

func TestSaveSettingsRejectsMissingExe(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.m.SaveSettings(config.Settings{SSHPath: `C:\nope\ssh.exe`}); err == nil {
		t.Fatal("want error")
	}
	if err := e.m.SaveSettings(config.Settings{Notifications: false, PlinkPath: os.Args[0]}); err != nil {
		t.Fatal(err)
	}
	if e.m.engine.PlinkExe != os.Args[0] || e.m.engine.SSHExe != engine.DefaultSSHExe || e.m.Settings().Notifications {
		t.Fatalf("engine %s %s", e.m.engine.PlinkExe, e.m.engine.SSHExe)
	}
}
