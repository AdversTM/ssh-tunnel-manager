//go:build integration

// Integration tests against the Docker sshd (test/sshd): password and
// passphrase auth through askpass / plink -pwfile, host key accept/reject/change,
// and a secret leak check (AC-8, AC-9).
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"

	"ssh-tunnel-manager/internal/askpass"
	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/logs"
	"ssh-tunnel-manager/internal/secrets"
	"ssh-tunnel-manager/internal/supervisor"
)

const (
	container   = "stm-sshd"
	itSession   = "ssh-tunnel-manager-itest-e2"
	pwuserPass  = "testpass"
	keyPassword = "keypass"
)

func TestMain(m *testing.M) {
	if askpass.IsHelperInvocation() {
		os.Exit(askpass.RunHelper(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func serverFingerprint(t *testing.T) string {
	t.Helper()
	return strings.Fields(run(t, "docker", "exec", container, "ssh-keygen", "-lf", "/etc/ssh/ssh_host_ed25519_key.pub"))[1]
}

type harness struct {
	e      *Engine
	sup    *supervisor.Supervisor
	prompt atomic.Int32
	accept atomic.Bool
	refs   []string
	mu     sync.Mutex
	lines  []string
	root   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if !strings.Contains(run(t, "docker", "ps", "--filter", "name="+container, "--format", "{{.Names}}"), container) {
		t.Fatalf("container %s not running: docker compose -f test/sshd/compose.yaml up -d", container)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := askpass.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsurePlinkSession(itSession, 5); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	h := &harness{root: filepath.Join(filepath.Dir(file), "..", "..")}
	h.accept.Store(true)
	h.e = &Engine{
		SSHExe: DefaultSSHExe, PlinkExe: DefaultPlinkExe, DataDir: t.TempDir(), HelperExe: self,
		Secrets: secrets.WinCred{}, Askpass: srv,
		HostKeyPrompt:       func(HostKeyRequest) bool { h.prompt.Add(1); return h.accept.Load() },
		Log:                 func(id, text string) { h.addLine("[engine] " + text) },
		ServerAliveInterval: 5, ServerAliveCountMax: 2, PlinkSession: itSession,
	}
	h.sup = supervisor.New(supervisor.Options{
		Prepare: h.e.Prepare,
		Delay:   func(int) time.Duration { return 500 * time.Millisecond },
		OnLine:  func(id string, l logs.Line) { h.addLine(l.Text) },
	})
	t.Cleanup(func() {
		h.sup.StopAll()
		srv.Close()
		for _, r := range h.refs {
			if err := (secrets.WinCred{}).Delete(r); err != nil {
				t.Error(err)
			}
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, `Software\SimonTatham\PuTTY\Sessions\`+itSession); err != nil {
			t.Error(err)
		}
		if t.Failed() {
			h.mu.Lock()
			t.Logf("output:\n%s", strings.Join(h.lines, "\n"))
			h.mu.Unlock()
		}
	})
	return h
}

func (h *harness) addLine(s string) {
	h.mu.Lock()
	h.lines = append(h.lines, s)
	h.mu.Unlock()
}

func (h *harness) tunnel(t *testing.T, b config.Backend, user string, m config.AuthMethod, secret string) config.Tunnel {
	t.Helper()
	b6 := make([]byte, 6)
	if _, err := rand.Read(b6); err != nil {
		t.Fatal(err)
	}
	id := "it-" + hex.EncodeToString(b6)
	tn := config.Tunnel{
		ID: id, Name: id, Backend: b, Host: "127.0.0.1", Port: 2222, User: user,
		Auth:    config.Auth{Method: m},
		Forward: config.Forward{Type: config.ForwardRemote, BindAddress: "127.0.0.1", BindPort: 18200 + int(b6[0]%50), TargetHost: "127.0.0.1", TargetPort: 9},
	}
	if b == config.BackendPlink {
		tn.HostKey.PlinkFingerprint = serverFingerprint(t)
	}
	if secret != "" {
		ref := CredentialRef(tn)
		if err := (secrets.WinCred{}).Set(ref, secret); err != nil {
			t.Fatal(err)
		}
		h.refs = append(h.refs, ref)
	}
	return tn
}

func (h *harness) wait(t *testing.T, id string, timeout time.Duration, what string, ok func(supervisor.Status) bool) supervisor.Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st, found := h.sup.Status(id); found && ok(st) {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	st, _ := h.sup.Status(id)
	t.Fatalf("timeout waiting for %s; last %+v", what, st)
	return supervisor.Status{}
}

func is(s supervisor.State) func(supervisor.Status) bool {
	return func(st supervisor.Status) bool { return st.State == s }
}

// AC-9: secret not in any process command line nor in app data files.
func assertNoLeak(t *testing.T, h *harness, secret string) {
	t.Helper()
	cmdlines := run(t, "powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | ForEach-Object { $_.CommandLine }")
	if strings.Contains(cmdlines, secret) {
		t.Fatal("secret visible in a process command line")
	}
	err := filepath.WalkDir(h.e.DataDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(data), secret) {
			return fmt.Errorf("secret found in %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(h.e.TmpDir(), "pw-*")); len(left) != 0 {
		t.Fatalf("password files left: %v", left)
	}
}

func TestIntegrationOpenSSHPasswordWithHostKeyPrompt(t *testing.T) {
	h := newHarness(t)
	tn := h.tunnel(t, config.BackendOpenSSH, "pwuser", config.AuthPassword, pwuserPass)
	h.sup.Start(tn)
	h.wait(t, tn.ID, 20*time.Second, "up", is(supervisor.Up))
	if n := h.prompt.Load(); n != 1 {
		t.Fatalf("host key prompts=%d want 1", n)
	}
	fp, err := h.e.KnownOpenSSHFingerprint(tn)
	if err != nil || fp != serverFingerprint(t) {
		t.Fatalf("known_hosts fingerprint %q err %v", fp, err)
	}
	assertNoLeak(t, h, pwuserPass)

	h.sup.Restart(tn.ID)
	time.Sleep(time.Second)
	h.wait(t, tn.ID, 20*time.Second, "up again", is(supervisor.Up))
	if n := h.prompt.Load(); n != 1 {
		t.Fatalf("known host asked again: prompts=%d", n)
	}
}

func TestIntegrationOpenSSHHostKeyRejected(t *testing.T) {
	h := newHarness(t)
	h.accept.Store(false)
	tn := h.tunnel(t, config.BackendOpenSSH, "pwuser", config.AuthPassword, pwuserPass)
	h.sup.Start(tn)
	st := h.wait(t, tn.ID, 20*time.Second, "error", is(supervisor.Error))
	if st.ErrorKind != "host-key-unknown" || h.prompt.Load() != 1 {
		t.Fatalf("status %+v prompts %d", st, h.prompt.Load())
	}
}

func TestIntegrationOpenSSHPassphraseKey(t *testing.T) {
	h := newHarness(t)
	tn := h.tunnel(t, config.BackendOpenSSH, "keyuser", config.AuthKey, keyPassword)
	tn.Auth.KeyPath = filepath.Join(h.root, "test", "keys", "client_test_pp")
	h.sup.Start(tn)
	h.wait(t, tn.ID, 20*time.Second, "up", is(supervisor.Up))
	assertNoLeak(t, h, keyPassword)
}

func TestIntegrationPlinkPasswordFile(t *testing.T) {
	h := newHarness(t)
	tn := h.tunnel(t, config.BackendPlink, "pwuser", config.AuthPassword, pwuserPass)
	h.sup.Start(tn)
	h.wait(t, tn.ID, 20*time.Second, "up", is(supervisor.Up))
	assertNoLeak(t, h, pwuserPass)
}

func TestIntegrationPlinkWrongPassword(t *testing.T) {
	h := newHarness(t)
	tn := h.tunnel(t, config.BackendPlink, "pwuser", config.AuthPassword, "wrong")
	h.sup.Start(tn)
	st := h.wait(t, tn.ID, 20*time.Second, "error", is(supervisor.Error))
	if st.ErrorKind != "auth" {
		t.Fatalf("status %+v", st)
	}
	if left, _ := filepath.Glob(filepath.Join(h.e.TmpDir(), "pw-*")); len(left) != 0 {
		t.Fatalf("password files left after failed auth: %v", left)
	}
}

// AC-8 (changed key): runs last because it rotates the container host key.
func TestIntegrationZChangedHostKey(t *testing.T) {
	h := newHarness(t)
	ssh := h.tunnel(t, config.BackendOpenSSH, "pwuser", config.AuthPassword, pwuserPass)
	plink := h.tunnel(t, config.BackendPlink, "pwuser", config.AuthPassword, pwuserPass)
	h.sup.Start(ssh)
	h.sup.Start(plink)
	h.wait(t, ssh.ID, 20*time.Second, "ssh up", is(supervisor.Up))
	h.wait(t, plink.ID, 20*time.Second, "plink up", is(supervisor.Up))
	h.sup.StopAll()
	oldFP := serverFingerprint(t)

	run(t, "docker", "exec", container, "rm", "-f", "/etc/ssh/ssh_host_ed25519_key", "/etc/ssh/ssh_host_ed25519_key.pub")
	run(t, "docker", "restart", container)
	time.Sleep(2 * time.Second)
	newFP := serverFingerprint(t)
	if newFP == oldFP {
		t.Fatal("host key did not rotate")
	}

	h.sup.Start(ssh)
	st := h.wait(t, ssh.ID, 20*time.Second, "ssh changed-key error", is(supervisor.Error))
	if st.ErrorKind != "host-key-changed" || st.Fingerprint != newFP {
		t.Fatalf("ssh status %+v (new %s)", st, newFP)
	}
	if known, err := h.e.KnownOpenSSHFingerprint(ssh); err != nil || known != oldFP {
		t.Fatalf("stored fp %q err %v, want old %s", known, err, oldFP)
	}
	if err := h.e.ForgetOpenSSHHostKey(ssh); err != nil {
		t.Fatal(err)
	}
	h.sup.Start(ssh)
	h.wait(t, ssh.ID, 20*time.Second, "ssh up after accepting new key", is(supervisor.Up))

	h.sup.Start(plink)
	st = h.wait(t, plink.ID, 20*time.Second, "plink changed-key error", is(supervisor.Error))
	if st.ErrorKind != "host-key-changed" || st.Fingerprint != newFP {
		t.Fatalf("plink status %+v (new %s)", st, newFP)
	}
	plink.HostKey.PlinkFingerprint = st.Fingerprint
	h.sup.Start(plink)
	h.wait(t, plink.ID, 20*time.Second, "plink up with new key", is(supervisor.Up))
}
