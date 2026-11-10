package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"

	"ssh-tunnel-manager/internal/askpass"
	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/secrets"
)

type memStore map[string]string

func (m memStore) Get(ref string) (string, error) {
	s, ok := m[ref]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return s, nil
}
func (m memStore) Set(ref, s string) error { m[ref] = s; return nil }
func (m memStore) Delete(ref string) error { delete(m, ref); return nil }

const hostPrompt = "The authenticity of host '[127.0.0.1]:2222 ([127.0.0.1]:2222)' can't be established.\n" +
	"ED25519 key fingerprint is SHA256:xKBMZBKqT6MDy9DjELSmt7wcVR2zWOCIDNVfONO0tLE.\n" +
	"Are you sure you want to continue connecting (yes/no/[fingerprint])? "

func newEngine(t *testing.T, accept bool) (*Engine, *[]HostKeyRequest) {
	t.Helper()
	var asked []HostKeyRequest
	srv, err := askpass.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	e := &Engine{
		SSHExe: DefaultSSHExe, PlinkExe: DefaultPlinkExe, DataDir: t.TempDir(), HelperExe: `C:\app.exe`,
		Secrets: memStore{"ssh-tunnel-manager:t1": "s3cret"}, Askpass: srv,
		HostKeyPrompt:       func(r HostKeyRequest) bool { asked = append(asked, r); return accept },
		ServerAliveInterval: 20, ServerAliveCountMax: 3, PlinkSession: DefaultPlinkSession,
	}
	return e, &asked
}

func tunnel(b config.Backend, m config.AuthMethod) config.Tunnel {
	return config.Tunnel{ID: "t1", Name: "dev", Backend: b, Host: "127.0.0.1", Port: 2222, User: "u",
		Auth: config.Auth{Method: m, KeyPath: "k", Agent: config.AgentPageant}}
}

func TestResponderMatchesPromptToAuthMethod(t *testing.T) {
	e, _ := newEngine(t, true)
	cases := []struct {
		method config.AuthMethod
		prompt string
		want   string
		ok     bool
	}{
		{config.AuthPassword, "pwuser@127.0.0.1's password: ", "s3cret", true},
		{config.AuthPassword, "Password: ", "s3cret", true},
		{config.AuthPassword, "Enter passphrase for key 'k': ", "", false},
		{config.AuthKey, "Enter passphrase for key 'test\\keys\\client_test_pp': ", "s3cret", true},
		{config.AuthKey, "pwuser@127.0.0.1's password: ", "", false},
		{config.AuthAgent, "pwuser@127.0.0.1's password: ", "", false},
		{config.AuthAgent, "Enter passphrase for key 'k': ", "", false},
		{config.AuthPassword, "Verification code: ", "", false},
	}
	for _, c := range cases {
		got, err := e.Responder(tunnel(config.BackendOpenSSH, c.method))(c.prompt)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s %q: got %q err %v", c.method, c.prompt, got, err)
		}
	}
}

func TestResponderMissingSecret(t *testing.T) {
	e, _ := newEngine(t, true)
	e.Secrets = memStore{}
	if _, err := e.Responder(tunnel(config.BackendOpenSSH, config.AuthPassword))("u@h's password: "); err == nil ||
		!strings.Contains(err.Error(), "no password") {
		t.Fatalf("err=%v", err)
	}
}

func TestResponderHostKey(t *testing.T) {
	for _, accept := range []bool{true, false} {
		e, asked := newEngine(t, accept)
		got, err := e.Responder(tunnel(config.BackendOpenSSH, config.AuthKey))(hostPrompt)
		want := map[bool]string{true: "yes", false: "no"}[accept]
		if err != nil || got != want {
			t.Fatalf("accept=%v got %q err %v", accept, got, err)
		}
		if len(*asked) != 1 || (*asked)[0].Fingerprint != "SHA256:xKBMZBKqT6MDy9DjELSmt7wcVR2zWOCIDNVfONO0tLE" ||
			(*asked)[0].Host != "[127.0.0.1]:2222" || (*asked)[0].TunnelName != "dev" {
			t.Fatalf("asked %+v", *asked)
		}
	}
}

func TestPrepareOpenSSH(t *testing.T) {
	e, _ := newEngine(t, true)
	o, cleanup, err := e.Prepare(tunnel(config.BackendOpenSSH, config.AuthPassword))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if o.Exe != DefaultSSHExe || o.StrictHostKeyCheck != "ask" || o.KnownHostsFile != filepath.Join(e.DataDir, "known_hosts") {
		t.Fatalf("opts %+v", o)
	}
	if !slices.Contains(o.AskpassEnv, "SSH_ASKPASS=C:\\app.exe") || !slices.Contains(o.AskpassEnv, "SSH_ASKPASS_REQUIRE=force") {
		t.Fatalf("env %q", o.AskpassEnv)
	}
	for _, kv := range o.AskpassEnv {
		if strings.Contains(kv, "s3cret") {
			t.Fatal("secret leaked into environment")
		}
	}
}

func TestPreparePlinkPasswordFile(t *testing.T) {
	e, _ := newEngine(t, true)
	o, cleanup, err := e.Prepare(tunnel(config.BackendPlink, config.AuthPassword))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(o.PwFile)
	if err != nil || string(data) != "s3cret\n" || filepath.Dir(o.PwFile) != e.TmpDir() {
		t.Fatalf("pwfile %q %q %v", o.PwFile, data, err)
	}
	if o.Exe != DefaultPlinkExe || o.PlinkSession != DefaultPlinkSession {
		t.Fatalf("opts %+v", o)
	}
	cleanup()
	if _, err := os.Stat(o.PwFile); !os.IsNotExist(err) {
		t.Fatal("password file not removed by cleanup")
	}
}

func TestPreparePlinkPasswordMissingSecret(t *testing.T) {
	e, _ := newEngine(t, true)
	e.Secrets = memStore{}
	if _, _, err := e.Prepare(tunnel(config.BackendPlink, config.AuthPassword)); err == nil {
		t.Fatal("want error without stored password")
	}
}

func TestAgentPipePageant(t *testing.T) {
	names, err := listPipes()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, "pageant.") }) {
		t.Skip("Pageant not running on this machine")
	}
	p, err := AgentPipe(config.AgentPageant)
	if err != nil || !strings.HasPrefix(p, `\\.\pipe\pageant.`) {
		t.Fatalf("pipe %q err %v", p, err)
	}
}

func TestEnsurePlinkSession(t *testing.T) {
	name := "ssh-tunnel-manager-unittest"
	path := `Software\SimonTatham\PuTTY\Sessions\` + name
	t.Cleanup(func() {
		if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil {
			t.Error(err)
		}
	})
	if err := EnsurePlinkSession(name, 20); err != nil {
		t.Fatal(err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("PingIntervalSecs")
	if err != nil || v != 20 {
		t.Fatalf("PingIntervalSecs=%d err %v", v, err)
	}
}
