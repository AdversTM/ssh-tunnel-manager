package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func validTunnel(t *testing.T) Tunnel {
	t.Helper()
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Tunnel{
		ID: "t1", Name: "dev-web", Backend: BackendOpenSSH,
		Host: "vps.example.com", Port: 22, User: "tunnel",
		Auth:    Auth{Method: AuthKey, KeyPath: key},
		Forward: Forward{Type: ForwardRemote, BindAddress: "127.0.0.1", BindPort: 8080, TargetHost: "127.0.0.1", TargetPort: 3000},
		Server:  Server{Mode: ServerNone},
	}
}

func TestValidateAcceptsValidTunnel(t *testing.T) {
	if err := validTunnel(t).Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Tunnel)
		want   string
	}{
		{"port zero", func(x *Tunnel) { x.Port = 0 }, "port must be 1-65535"},
		{"port too big", func(x *Tunnel) { x.Port = 65536 }, "port must be 1-65535"},
		{"empty host", func(x *Tunnel) { x.Host = " " }, "host is required"},
		{"empty user", func(x *Tunnel) { x.User = "" }, "user is required"},
		{"bad bind address", func(x *Tunnel) { x.Forward.BindAddress = "vps.example.com" }, "bind address"},
		{"bad bind port", func(x *Tunnel) { x.Forward.BindPort = -1 }, "bind port"},
		{"bad target port", func(x *Tunnel) { x.Forward.TargetPort = 70000 }, "target port"},
		{"bad forward type", func(x *Tunnel) { x.Forward.Type = "D" }, "forward type"},
		{"missing key file", func(x *Tunnel) { x.Auth.KeyPath = filepath.Join(t.TempDir(), "nope") }, "key file not accessible"},
		{"plink with openssh agent", func(x *Tunnel) {
			x.Backend = BackendPlink
			x.Auth = Auth{Method: AuthAgent, Agent: AgentOpenSSHAgent}
		}, "plink supports only the pageant agent"},
		{"bad backend", func(x *Tunnel) { x.Backend = "putty" }, "backend must be"},
		{"caddy without domain", func(x *Tunnel) { x.Server.Mode = ServerSSHDCaddy }, "domain is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tun := validTunnel(t)
			c.mutate(&tun)
			err := tun.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestValidateAcceptsIPv6AndLocalhost(t *testing.T) {
	for _, a := range []string{"::1", "localhost", "0.0.0.0"} {
		tun := validTunnel(t)
		tun.Forward.BindAddress = a
		if err := tun.Validate(); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
}

func TestConfigValidateDuplicateIDs(t *testing.T) {
	a := validTunnel(t)
	cfg := Config{Tunnels: []Tunnel{a, a}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("want duplicate id error, got %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	cfg := Default()
	cfg.Settings.Autostart = true
	cfg.Tunnels = append(cfg.Tunnels, validTunnel(t))
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, cfg)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadCorruptBacksUpAndReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Fatalf("want default config, got %+v", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt file should be moved away, stat err=%v", err)
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("want 1 backup, got %v (%v)", backups, err)
	}
	data, err := os.ReadFile(backups[0])
	if err != nil || string(data) != "{not json" {
		t.Fatalf("backup content mismatch: %q %v", data, err)
	}
}
