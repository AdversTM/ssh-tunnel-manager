//go:build integration

// Integration tests against the Docker sshd from test/sshd (docker compose up -d)
// using keys from test/sshd/gen-keys.ps1. Run: go test -tags integration ./internal/supervisor/
package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ssh-tunnel-manager/internal/backend"
	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/logs"
)

const (
	container    = "stm-sshd"
	dockerNet    = "stm-test_default"
	plinkSession = "ssh-tunnel-manager-itest"
	sessionKey   = `HKCU\Software\SimonTatham\PuTTY\Sessions\` + plinkSession
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller unknown")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func dockerExec(t *testing.T, args ...string) string {
	t.Helper()
	return run(t, "docker", append([]string{"exec", container}, args...)...)
}

type env struct {
	root, knownHosts, fingerprint string
	mu                            sync.Mutex
	lines                         []string
}

func setup(t *testing.T) *env {
	t.Helper()
	if !strings.Contains(run(t, "docker", "ps", "--filter", "name="+container, "--format", "{{.Names}}"), container) {
		t.Fatalf("container %s not running: docker compose -f test/sshd/compose.yaml up -d", container)
	}
	e := &env{root: repoRoot(t), knownHosts: filepath.Join(t.TempDir(), "known_hosts")}
	e.fingerprint = strings.Fields(dockerExec(t, "ssh-keygen", "-lf", "/etc/ssh/ssh_host_ed25519_key.pub"))[1]
	run(t, "reg", "add", sessionKey, "/v", "PingIntervalSecs", "/t", "REG_DWORD", "/d", "5", "/f")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "reg", "delete", sessionKey, "/f").CombinedOutput(); err != nil {
			t.Errorf("cleanup reg session: %v %s", err, out)
		}
		if t.Failed() {
			e.mu.Lock()
			t.Logf("client output:\n%s", strings.Join(e.lines, "\n"))
			e.mu.Unlock()
		}
	})
	return e
}

func (e *env) sup(delay func(int) time.Duration) *Supervisor {
	return New(Options{
		Prepare: func(tn config.Tunnel) (backend.Options, func(), error) {
			exe := `C:\Windows\System32\OpenSSH\ssh.exe`
			if tn.Backend == config.BackendPlink {
				exe = `C:\Program Files\PuTTY\plink.exe`
			}
			return backend.Options{
				Exe: exe, KnownHostsFile: e.knownHosts, StrictHostKeyCheck: "accept-new",
				ServerAliveInterval: 5, ServerAliveCountMax: 2, PlinkSession: plinkSession,
			}, func() {}, nil
		},
		Delay: delay,
		OnLine: func(id string, l logs.Line) {
			e.mu.Lock()
			e.lines = append(e.lines, fmt.Sprintf("%s [%s] %s", l.Time.Format("15:04:05.000"), id, l.Text))
			e.mu.Unlock()
		},
	})
}

func (e *env) tunnel(b config.Backend, user string, ft config.ForwardType, bindPort, targetPort int) config.Tunnel {
	key := filepath.Join(e.root, "test", "keys", "client_test")
	if b == config.BackendPlink {
		key += ".ppk"
	}
	return config.Tunnel{
		ID: "it", Name: "it", Backend: b, Host: "127.0.0.1", Port: 2222, User: user,
		Auth:    config.Auth{Method: config.AuthKey, KeyPath: key},
		Forward: config.Forward{Type: ft, BindAddress: "127.0.0.1", BindPort: bindPort, TargetHost: "127.0.0.1", TargetPort: targetPort},
		HostKey: config.HostKey{PlinkFingerprint: e.fingerprint},
	}
}

func waitFor(t *testing.T, s *Supervisor, timeout time.Duration, what string, ok func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st, found := s.Status("it"); found && ok(st) {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	st, _ := s.Status("it")
	t.Fatalf("timeout waiting for %s; last status %+v", what, st)
	return Status{}
}

func state(want State) func(Status) bool { return func(s Status) bool { return s.State == want } }

func localHTTP(t *testing.T, body string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

var backends = []config.Backend{config.BackendOpenSSH, config.BackendPlink}

// AC-2, AC-3: -R tunnel carries traffic from the server to the local service.
func TestIntegrationRemoteForward(t *testing.T) {
	for i, b := range backends {
		t.Run(string(b), func(t *testing.T) {
			e := setup(t)
			target := localHTTP(t, "hello-from-windows")
			remote := 18100 + i
			s := e.sup(nil)
			defer s.StopAll()
			s.Start(e.tunnel(b, "keyuser", config.ForwardRemote, remote, target))
			waitFor(t, s, 15*time.Second, "up", state(Up))
			got := dockerExec(t, "wget", "-qO-", fmt.Sprintf("http://127.0.0.1:%d/", remote))
			if got != "hello-from-windows" {
				t.Fatalf("through tunnel got %q", got)
			}
		})
	}
}

// AC-3: -L tunnel reaches the server side (sshd banner on container port 22).
func TestIntegrationLocalForward(t *testing.T) {
	for _, b := range backends {
		t.Run(string(b), func(t *testing.T) {
			e := setup(t)
			local := freePort(t)
			s := e.sup(nil)
			defer s.StopAll()
			s.Start(e.tunnel(b, "keyuser", config.ForwardLocal, local, 22))
			waitFor(t, s, 15*time.Second, "up", state(Up))
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", local), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			banner, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil || !strings.HasPrefix(banner, "SSH-2.0-OpenSSH") {
				t.Fatalf("banner %q err %v", banner, err)
			}
		})
	}
}

// AC-4: killed client is restarted within 5 s.
func TestIntegrationKilledClientReconnects(t *testing.T) {
	e := setup(t)
	s := e.sup(nil)
	defer s.StopAll()
	s.Start(e.tunnel(config.BackendOpenSSH, "keyuser", config.ForwardRemote, 18110, localHTTP(t, "x")))
	first := waitFor(t, s, 15*time.Second, "up", state(Up))
	p, err := os.FindProcess(first.PID)
	if err != nil {
		t.Fatal(err)
	}
	killed := time.Now()
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	again := waitFor(t, s, 10*time.Second, "up with new pid", func(st Status) bool {
		return st.State == Up && st.PID != first.PID
	})
	if d := time.Since(killed); d > 5*time.Second {
		t.Fatalf("reconnect took %v", d)
	}
	t.Logf("pid %d -> %d after %v", first.PID, again.PID, time.Since(killed).Round(time.Millisecond))
}

// AC-5: network outage -> backoff with growing attempts -> recovers.
func TestIntegrationNetworkOutage(t *testing.T) {
	e := setup(t)
	s := e.sup(nil)
	defer s.StopAll()
	s.Start(e.tunnel(config.BackendOpenSSH, "keyuser", config.ForwardRemote, 18120, localHTTP(t, "x")))
	waitFor(t, s, 15*time.Second, "up", state(Up))
	run(t, "docker", "network", "disconnect", dockerNet, container)
	netOn := sync.OnceFunc(func() { run(t, "docker", "network", "connect", dockerNet, container) })
	defer netOn()
	waitFor(t, s, 30*time.Second, "backoff", state(Backoff))
	st := waitFor(t, s, 30*time.Second, "attempt >= 3", func(st Status) bool { return st.Attempt >= 3 })
	t.Logf("during outage: %+v", st)
	netOn()
	waitFor(t, s, 60*time.Second, "up after recovery", state(Up))
}

// AC-6: remote port taken -> remote-port-busy + backoff, recovers when freed.
func TestIntegrationRemotePortBusy(t *testing.T) {
	for i, b := range backends {
		t.Run(string(b), func(t *testing.T) {
			e := setup(t)
			remote := 18130 + i
			dockerExec(t, "sh", "-c", fmt.Sprintf("socat TCP-LISTEN:%d,bind=127.0.0.1,fork,reuseaddr SYSTEM:'echo busy' >/dev/null 2>&1 &", remote))
			freed := sync.OnceFunc(func() {
				dockerExec(t, "sh", "-c", fmt.Sprintf("pkill -f '[T]CP-LISTEN:%d,'; true", remote))
			})
			defer freed()
			time.Sleep(500 * time.Millisecond)
			s := e.sup(func(int) time.Duration { return 500 * time.Millisecond })
			defer s.StopAll()
			s.Start(e.tunnel(b, "keyuser", config.ForwardRemote, remote, localHTTP(t, "x")))
			st := waitFor(t, s, 15*time.Second, "backoff remote-port-busy", func(st Status) bool {
				return st.State == Backoff && st.ErrorKind == "remote-port-busy"
			})
			t.Logf("busy: %s", st.LastError)
			freed()
			waitFor(t, s, 15*time.Second, "up after port freed", state(Up))
		})
	}
}

// AC-7: rejected auth -> error, no retry.
func TestIntegrationAuthFailure(t *testing.T) {
	for _, b := range backends {
		t.Run(string(b), func(t *testing.T) {
			e := setup(t)
			var starts atomic.Int32
			s := e.sup(nil)
			defer s.StopAll()
			s.o.OnStatus = func(st Status) {
				if st.State == Connecting && st.PID != 0 {
					starts.Add(1)
				}
			}
			s.Start(e.tunnel(b, "pwuser", config.ForwardRemote, 18140, 9))
			st := waitFor(t, s, 15*time.Second, "error", state(Error))
			if st.ErrorKind != "auth" {
				t.Fatalf("kind=%s err=%s", st.ErrorKind, st.LastError)
			}
			time.Sleep(2 * time.Second)
			if n := starts.Load(); n != 1 {
				t.Fatalf("connect attempts=%d, auth must not retry", n)
			}
			t.Logf("auth error: %s", st.LastError)
		})
	}
}

// Local port already taken for -L -> local-port-busy error.
func TestIntegrationLocalPortBusy(t *testing.T) {
	for _, b := range backends {
		t.Run(string(b), func(t *testing.T) {
			e := setup(t)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			s := e.sup(nil)
			defer s.StopAll()
			s.Start(e.tunnel(b, "keyuser", config.ForwardLocal, ln.Addr().(*net.TCPAddr).Port, 22))
			st := waitFor(t, s, 15*time.Second, "local-port-busy", func(st Status) bool {
				return st.ErrorKind == "local-port-busy"
			})
			t.Logf("local busy: state=%s %s", st.State, st.LastError)
		})
	}
}
