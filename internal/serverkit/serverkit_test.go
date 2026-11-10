package serverkit

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh-tunnel-manager/internal/config"
)

var update = flag.Bool("update", false, "rewrite testdata golden files")

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBkYk4V5vQ0n3cX2l9hHq2l0bJm2yJ8Qm8r7tHqf1c2d user@pc"

func tunnel(mode config.ServerMode, fwd config.ForwardType, bind string) config.Tunnel {
	return config.Tunnel{
		ID: "id1", Name: "Dev Web", Backend: config.BackendOpenSSH, Host: "vps.example.com", Port: 22, User: "tunnel",
		Auth:    config.Auth{Method: config.AuthKey, KeyPath: `C:\keys\k`},
		Forward: config.Forward{Type: fwd, BindAddress: bind, BindPort: 8080, TargetHost: "127.0.0.1", TargetPort: 3000},
		Server:  config.Server{Mode: mode, Domain: "app.example.com"},
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("%s differs from golden; run go test ./internal/serverkit -update and review the diff", name)
	}
}

func TestGenerateGolden(t *testing.T) {
	for _, mode := range []config.ServerMode{config.ServerNone, config.ServerSSHD, config.ServerSSHDCaddy} {
		for _, bind := range []string{"127.0.0.1", "0.0.0.0"} {
			name := strings.ReplaceAll(string(mode), "+", "-") + "-R-" + bind
			t.Run(name, func(t *testing.T) {
				kit, err := Generate(tunnel(mode, config.ForwardRemote, bind), testKey)
				if err != nil {
					t.Fatal(err)
				}
				golden(t, name+".txt", "== instructions\n"+kit.Instructions+"== script "+kit.ScriptName+"\n"+kit.Script+"== caddyfile\n"+kit.Caddyfile)
				if mode == config.ServerNone {
					if kit.Script != "" || kit.Caddyfile != "" {
						t.Fatal("mode none must not produce a script")
					}
					return
				}
				for _, want := range []string{
					"SPEC='" + bind + ":8080'",
					`AK_OPT='permitlisten="` + bind + `:8080"'`,
					"KEYWORD='PermitListen'",
					"PUBKEY='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBkYk4V5vQ0n3cX2l9hHq2l0bJm2yJ8Qm8r7tHqf1c2d'",
				} {
					if !strings.Contains(kit.Script, want) {
						t.Errorf("script misses %s", want)
					}
				}
				if strings.Count(kit.Script, "permitlisten=") != 1 {
					t.Error("permitlisten must appear exactly once")
				}
				if mode == config.ServerSSHDCaddy && kit.Caddyfile != "app.example.com {\n\treverse_proxy 127.0.0.1:8080\n}\n" {
					t.Errorf("caddyfile %q", kit.Caddyfile)
				}
			})
		}
	}
	t.Run("sshd-L", func(t *testing.T) {
		kit, err := Generate(tunnel(config.ServerSSHD, config.ForwardLocal, "127.0.0.1"), "")
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "sshd-L.txt", "== instructions\n"+kit.Instructions+"== script "+kit.ScriptName+"\n"+kit.Script)
		if !strings.Contains(kit.Script, "SPEC='127.0.0.1:3000'") || !strings.Contains(kit.Script, `permitopen="127.0.0.1:3000"`) {
			t.Error("-L must allow exactly the target via PermitOpen")
		}
	})
}

func TestGenerateRejects(t *testing.T) {
	cases := map[string]func(*config.Tunnel){
		"root user":     func(t *config.Tunnel) { t.User = "root" },
		"bad user":      func(t *config.Tunnel) { t.User = "a b;rm" },
		"bad domain":    func(t *config.Tunnel) { t.Server.Domain = "x'; rm -rf /" },
		"caddy with -L": func(t *config.Tunnel) { t.Forward.Type = config.ForwardLocal },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			tn := tunnel(config.ServerSSHDCaddy, config.ForwardRemote, "127.0.0.1")
			mut(&tn)
			if _, err := Generate(tn, testKey); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	if _, err := Generate(tunnel(config.ServerSSHD, config.ForwardRemote, "127.0.0.1"), "not a key"); err == nil {
		t.Fatal("bad public key accepted")
	}
}

func TestShellQuoteNeutralizesQuotes(t *testing.T) {
	if got := shellQuote(`a'b $(x)`); got != `'a'\''b $(x)'` {
		t.Fatal(got)
	}
}

func TestPublicKeyFromFiles(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "k")
	if err := os.WriteFile(key+".pub", []byte(testKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := PublicKey(key)
	if err != nil || got != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBkYk4V5vQ0n3cX2l9hHq2l0bJm2yJ8Qm8r7tHqf1c2d" {
		t.Fatalf("%q %v", got, err)
	}
	ppk := filepath.Join(dir, "k.ppk")
	body := "PuTTY-User-Key-File-3: ssh-ed25519\nEncryption: none\nComment: x\nPublic-Lines: 2\nAAAAC3NzaC1lZDI1NTE5AAAAIBkYk4V5\nvQ0n3cX2l9hHq2l0bJm2yJ8Qm8r7tHqf1c2d\nPrivate-Lines: 1\nAAAA\n"
	if err := os.WriteFile(ppk, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got2, err := PublicKey(ppk); err != nil || got2 != got {
		t.Fatalf("ppk %q %v", got2, err)
	}
}
