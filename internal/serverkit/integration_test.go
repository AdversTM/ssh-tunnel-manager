//go:build integration

// Runs generated setup scripts in a clean Debian + OpenSSH container
// (test/serverkit/Dockerfile, image stm-serverkit-test) and validates the
// Caddy block with the official caddy image.
package serverkit

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ssh-tunnel-manager/internal/config"
)

const (
	image     = "stm-serverkit-test"
	container = "stm-serverkit-it"
	sshPort   = "2223"
)

func docker(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustDocker(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, err := docker(t, stdin, args...)
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return out
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func itTunnel(port int, mode config.ServerMode) config.Tunnel {
	return config.Tunnel{
		ID: "it", Name: "it", Backend: config.BackendOpenSSH, Host: "127.0.0.1", Port: 2223, User: "tunnel",
		Auth:    config.Auth{Method: config.AuthKey},
		Forward: config.Forward{Type: config.ForwardRemote, BindAddress: "127.0.0.1", BindPort: port, TargetHost: "127.0.0.1", TargetPort: 9},
		Server:  config.Server{Mode: mode, Domain: "app.example.com"},
	}
}

func runScript(t *testing.T, kit Kit) string {
	t.Helper()
	return mustDocker(t, kit.Script, "exec", "-i", container, "bash", "-s")
}

// sshForward starts ssh.exe with one -R forward and reports whether it is still
// running after settle (forward accepted) plus its output.
func sshForward(t *testing.T, key string, port string, settle time.Duration) (bool, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, `C:\Windows\System32\OpenSSH\ssh.exe`,
		"-F", "NUL", "-N", "-T", "-p", sshPort, "-l", "tunnel",
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=NUL", "-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "ExitOnForwardFailure=yes",
		"-o", "ConnectTimeout=10", "-i", key,
		"-R", "127.0.0.1:"+port+":127.0.0.1:9", "127.0.0.1")
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
		return false, out.String()
	case <-time.After(settle):
	}
	listening := mustDocker(t, "", "exec", container, "ss", "-ltn")
	cancel()
	<-done
	if !strings.Contains(listening, "127.0.0.1:"+port) {
		t.Fatalf("ssh running but port %s not listening:\n%s", port, listening)
	}
	return true, out.String()
}

func TestSetupScriptInContainer(t *testing.T) {
	if _, err := docker(t, "", "image", "inspect", image); err != nil {
		t.Fatalf("missing image: docker build -t %s test/serverkit", image)
	}
	_, _ = docker(t, "", "rm", "-f", container)
	mustDocker(t, "", "run", "-d", "--name", container, "-p", "127.0.0.1:"+sshPort+":22", image)
	t.Cleanup(func() { _, _ = docker(t, "", "rm", "-f", container) })

	key := filepath.Join(repoRoot(), "test", "keys", "client_test")
	pub, err := PublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	kit, err := Generate(itTunnel(18500, config.ServerSSHD), pub)
	if err != nil {
		t.Fatal(err)
	}
	runScript(t, kit)
	out := runScript(t, kit)
	if !strings.Contains(out, "done: tunnel may forward 127.0.0.1:18500") {
		t.Fatalf("second run output:\n%s", out)
	}
	mustDocker(t, "", "exec", container, "/usr/sbin/sshd", "-t")
	count := func(pattern, file string) string {
		return strings.TrimSpace(mustDocker(t, "", "exec", container, "sh", "-c", "grep -c -- '"+pattern+"' "+file+" || true"))
	}
	if c := count("ssh-tunnel-manager$", "/home/tunnel/.ssh/authorized_keys"); c != "1" {
		t.Fatalf("authorized_keys entries after 2 runs: %s", c)
	}
	if c := count("^Match User tunnel", "/etc/ssh/sshd_config.d/ssh-tunnel-manager-tunnel.conf"); c != "1" {
		t.Fatalf("Match blocks after 2 runs: %s", c)
	}

	if up, out := sshForward(t, key, "18501", 4*time.Second); up || !strings.Contains(out, "remote port forwarding failed") {
		t.Fatalf("port outside permitlisten must be refused; up=%v\n%s", up, out)
	}
	if up, out := sshForward(t, key, "18500", 4*time.Second); !up {
		t.Fatalf("allowed port must come up:\n%s", out)
	}

	second, err := Generate(itTunnel(18502, config.ServerSSHD), pub)
	if err != nil {
		t.Fatal(err)
	}
	runScript(t, second)
	conf := mustDocker(t, "", "exec", container, "cat", "/etc/ssh/sshd_config.d/ssh-tunnel-manager-tunnel.conf")
	if !strings.Contains(conf, "PermitListen 127.0.0.1:18500 127.0.0.1:18502") {
		t.Fatalf("second tunnel must extend PermitListen:\n%s", conf)
	}
	if c := count("ssh-tunnel-manager$", "/home/tunnel/.ssh/authorized_keys"); c != "1" {
		t.Fatalf("same key must stay one line, got %s", c)
	}
	for _, port := range []string{"18500", "18502"} {
		if up, out := sshForward(t, key, port, 4*time.Second); !up {
			t.Fatalf("port %s after second tunnel:\n%s", port, out)
		}
	}

	caddyKit, err := Generate(itTunnel(18500, config.ServerSSHDCaddy), pub)
	if err != nil {
		t.Fatal(err)
	}
	if out := runScript(t, caddyKit); !strings.Contains(out, "reverse_proxy 127.0.0.1:18500") {
		t.Fatalf("without Caddy the script must print the site block:\n%s", out)
	}
}

func TestCaddyfileValidates(t *testing.T) {
	kit, err := Generate(itTunnel(18500, config.ServerSSHDCaddy), "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := docker(t, kit.Caddyfile, "run", "-i", "--rm", "caddy:2-alpine", "sh", "-c",
		"cat > /tmp/Caddyfile && caddy validate --config /tmp/Caddyfile --adapter caddyfile")
	if err != nil || !strings.Contains(out, "Valid configuration") {
		t.Fatalf("caddy validate: %v\n%s", err, out)
	}
}
