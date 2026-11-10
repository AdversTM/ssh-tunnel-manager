package backend

import (
	"reflect"
	"strings"
	"testing"

	"ssh-tunnel-manager/internal/config"
)

func tunnel(b config.Backend, m config.AuthMethod, ft config.ForwardType) config.Tunnel {
	return config.Tunnel{
		ID: "t", Name: "t", Backend: b, Host: "vps", Port: 2222, User: "tun",
		Auth:    config.Auth{Method: m, KeyPath: `C:\keys\k`, Agent: config.AgentPageant},
		Forward: config.Forward{Type: ft, BindAddress: "127.0.0.1", BindPort: 8080, TargetHost: "127.0.0.1", TargetPort: 3000},
		HostKey: config.HostKey{PlinkFingerprint: "SHA256:abc"},
	}
}

var opts = Options{
	Exe: "ssh.exe", KnownHostsFile: `C:\Users\a b\kh`, ServerAliveInterval: 20, ServerAliveCountMax: 3,
	AgentPipe: `\\.\pipe\pageant.u.x`, PwFile: `C:\tmp\pw`, PlinkSession: "ssh-tunnel-manager",
}

func TestOpenSSHCommand(t *testing.T) {
	base := []string{"-F", "NUL", "-N", "-T", "-v", "-p", "2222", "-l", "tun",
		"-o", "ExitOnForwardFailure=yes", "-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=20", "-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=yes", "-o", `UserKnownHostsFile="C:/Users/a b/kh"`,
		"-o", "NumberOfPasswordPrompts=1"}
	cases := []struct {
		name    string
		m       config.AuthMethod
		ft      config.ForwardType
		askpass []string
		tail    []string
	}{
		{"key R", config.AuthKey, config.ForwardRemote, nil, []string{"-o", "BatchMode=yes",
			"-o", "PreferredAuthentications=publickey", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none",
			"-i", `C:\keys\k`, "-R", "127.0.0.1:8080:127.0.0.1:3000", "vps"}},
		{"agent L", config.AuthAgent, config.ForwardLocal, nil, []string{"-o", "BatchMode=yes",
			"-o", "PreferredAuthentications=publickey", "-o", "IdentityAgent=//./pipe/pageant.u.x",
			"-L", "127.0.0.1:8080:127.0.0.1:3000", "vps"}},
		{"password R askpass", config.AuthPassword, config.ForwardRemote, []string{"SSH_ASKPASS=x"}, []string{"-o", "BatchMode=no",
			"-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "PubkeyAuthentication=no",
			"-R", "127.0.0.1:8080:127.0.0.1:3000", "vps"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := opts
			o.AskpassEnv = c.askpass
			got := OpenSSH{}.Command(tunnel(config.BackendOpenSSH, c.m, c.ft), o)
			want := append(append([]string{}, base...), c.tail...)
			if !reflect.DeepEqual(got.Args, want) {
				t.Fatalf("args\n got %q\nwant %q", got.Args, want)
			}
			if got.Exe != "ssh.exe" || !reflect.DeepEqual(got.Env, c.askpass) {
				t.Fatalf("exe/env %q %q", got.Exe, got.Env)
			}
		})
	}
}

func TestPlinkCommand(t *testing.T) {
	base := []string{"-load", "ssh-tunnel-manager", "-ssh", "-batch", "-N", "-v", "-P", "2222", "-l", "tun", "-hostkey", "SHA256:abc"}
	cases := []struct {
		name string
		m    config.AuthMethod
		ft   config.ForwardType
		tail []string
	}{
		{"key R", config.AuthKey, config.ForwardRemote, []string{"-noagent", "-i", `C:\keys\k`, "-R", "127.0.0.1:8080:127.0.0.1:3000", "vps"}},
		{"agent L", config.AuthAgent, config.ForwardLocal, []string{"-agent", "-L", "127.0.0.1:8080:127.0.0.1:3000", "vps"}},
		{"password R", config.AuthPassword, config.ForwardRemote, []string{"-noagent", "-pwfile", `C:\tmp\pw`, "-R", "127.0.0.1:8080:127.0.0.1:3000", "vps"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := opts
			o.Exe = "plink.exe"
			got := Plink{}.Command(tunnel(config.BackendPlink, c.m, c.ft), o)
			want := append(append([]string{}, base...), c.tail...)
			if !reflect.DeepEqual(got.Args, want) {
				t.Fatalf("args\n got %q\nwant %q", got.Args, want)
			}
		})
	}
}

func TestNoSecretInArgs(t *testing.T) {
	tun := tunnel(config.BackendPlink, config.AuthPassword, config.ForwardRemote)
	for _, b := range []Backend{OpenSSH{}, Plink{}} {
		c := b.Command(tun, opts)
		if strings.Contains(strings.Join(c.Args, " "), "-pw ") {
			t.Fatalf("%T passes password on command line: %q", b, c.Args)
		}
	}
}

func TestForwardSpecIPv6(t *testing.T) {
	_, spec := forwardSpec(config.Forward{Type: config.ForwardRemote, BindAddress: "::1", BindPort: 80, TargetHost: "localhost", TargetPort: 3000})
	if spec != "[::1]:80:localhost:3000" {
		t.Fatalf("spec=%s", spec)
	}
}

type lineCase struct {
	line string
	want LineInfo
}

func runLines(t *testing.T, b Backend, cases []lineCase) {
	t.Helper()
	for _, c := range cases {
		if got := b.ClassifyLine(c.line); got != c.want {
			t.Errorf("%T %q\n got %+v\nwant %+v", b, c.line, got, c.want)
		}
	}
}

// Lines captured from OpenSSH_for_Windows_9.5p1.
func TestOpenSSHClassify(t *testing.T) {
	runLines(t, OpenSSH{}, []lineCase{
		{"debug1: remote forward success for: listen 127.0.0.1:18080, connect 127.0.0.1:18081", LineInfo{Up: true}},
		{"debug1: Local forwarding listening on 127.0.0.1 port 18082.", LineInfo{Up: true}},
		{"Error: remote port forwarding failed for listen port 18080", LineInfo{Failure: KindRemotePortBusy}},
		{"pwuser@127.0.0.1: Permission denied (publickey,password).", LineInfo{Failure: KindAuth}},
		{"No ED25519 host key is known for [127.0.0.1]:2222 and you have requested strict checking.", LineInfo{Failure: KindHostKeyUnknown}},
		{"Host key verification failed.", LineInfo{Failure: KindHostKeyUnknown}},
		{"@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @", LineInfo{Failure: KindHostKeyChanged}},
		{"Host key for [127.0.0.1]:2222 has changed and you have requested strict checking.", LineInfo{Failure: KindHostKeyChanged}},
		{"SHA256:EBzbLTwm81OXNzrr0rX1rLmnWRz3ba8v9K4hyJjmA1Y.", LineInfo{Fingerprint: "SHA256:EBzbLTwm81OXNzrr0rX1rLmnWRz3ba8v9K4hyJjmA1Y"}},
		{"Connection to 127.0.0.1 closed by remote host.", LineInfo{Failure: KindNetwork}},
		{"Timeout, server 127.0.0.1 not responding.", LineInfo{Failure: KindNetwork}},
		{"ssh: connect to host 127.0.0.1 port 2223: Connection refused", LineInfo{Failure: KindNetwork}},
		{"Authenticated to 127.0.0.1 ([127.0.0.1]:2222) using \"password\".", LineInfo{Authenticated: true}},
		{"debug1: Connection established.", LineInfo{Debug: true}},
		// captured by integration tests
		{"channel_setup_fwd_listener_tcpip: cannot listen to port: 62913", LineInfo{Failure: KindLocalPortBusy}},
		{"kex_exchange_identification: Connection closed by remote host", LineInfo{Failure: KindNetwork}},
		{"OpenSSH_for_Windows_9.5p1, LibreSSL 3.8.2", LineInfo{}},
	})
}

// Lines captured from plink 0.78.
func TestPlinkClassify(t *testing.T) {
	runLines(t, Plink{}, []lineCase{
		{"Remote port forwarding from 127.0.0.1:18080 enabled", LineInfo{Up: true}},
		{"Remote port forwarding from 127.0.0.1:18080 refused", LineInfo{Failure: KindRemotePortBusy}},
		{"FATAL ERROR: Cannot confirm a host key in batch mode", LineInfo{Failure: KindHostKeyUnknown}},
		{"FATAL ERROR: Host key not in manually configured list", LineInfo{Failure: KindHostKeyChanged}},
		{"FATAL ERROR: Configured password was not accepted", LineInfo{Failure: KindAuth}},
		{"FATAL ERROR: Cannot answer interactive prompts in batch mode", LineInfo{Failure: KindAuth}},
		{"FATAL ERROR: Remote side unexpectedly closed network connection", LineInfo{Failure: KindNetwork}},
		{"ssh-ed25519 255 SHA256:EBzbLTwm81OXNzrr0rX1rLmnWRz3ba8v9K4hyJjmA1Y", LineInfo{Debug: true, Fingerprint: "SHA256:EBzbLTwm81OXNzrr0rX1rLmnWRz3ba8v9K4hyJjmA1Y"}},
		{"  ssh-ed25519 255 SHA256:EBzbLTwm81OXNzrr0rX1rLmnWRz3ba8v9K4hyJjmA1Y", LineInfo{Debug: true, Fingerprint: "SHA256:EBzbLTwm81OXNzrr0rX1rLmnWRz3ba8v9K4hyJjmA1Y"}},
		{"Initialised AES-256 SDCTR (AES-NI accelerated) outbound encryption", LineInfo{Debug: true}},
		{"Access granted", LineInfo{Authenticated: true}},
		// captured by integration tests
		{"Local port 127.0.0.1:62936 forwarding to 127.0.0.1:22 failed: Network error: Address already in use", LineInfo{Failure: KindLocalPortBusy}},
		{"Server refused our key", LineInfo{Failure: KindAuth}},
	})
}

func TestKindPolicy(t *testing.T) {
	for _, k := range []Kind{KindNone, KindNetwork, KindRemotePortBusy} {
		if !k.Retryable() {
			t.Errorf("%v should retry", k)
		}
	}
	for _, k := range []Kind{KindAuth, KindHostKeyUnknown, KindHostKeyChanged, KindConfig, KindLocalPortBusy} {
		if k.Retryable() {
			t.Errorf("%v must not retry", k)
		}
	}
}
