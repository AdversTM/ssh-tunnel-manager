package backend

import (
	"net"
	"strconv"

	"ssh-tunnel-manager/internal/config"
)

// Kind classifies why a connection attempt failed. Higher values are more
// specific and win when several failure lines appear in one attempt.
type Kind int

const (
	KindNone Kind = iota
	KindNetwork
	KindRemotePortBusy
	KindLocalPortBusy
	KindConfig
	KindAuth
	KindHostKeyUnknown
	KindHostKeyChanged
)

func (k Kind) String() string {
	switch k {
	case KindNone:
		return "none"
	case KindNetwork:
		return "network"
	case KindRemotePortBusy:
		return "remote-port-busy"
	case KindLocalPortBusy:
		return "local-port-busy"
	case KindConfig:
		return "config"
	case KindAuth:
		return "auth"
	case KindHostKeyUnknown:
		return "host-key-unknown"
	case KindHostKeyChanged:
		return "host-key-changed"
	}
	return "unknown"
}

// Retryable reports whether the supervisor should reconnect with backoff.
// Auth, host key and config problems need user action; retrying auth failures
// would also risk a fail2ban ban on the server.
func (k Kind) Retryable() bool {
	return k == KindNone || k == KindNetwork || k == KindRemotePortBusy
}

// KillsProcess reports whether the supervisor must kill the client itself:
// plink keeps running after a refused forward.
func (k Kind) KillsProcess() bool {
	return k == KindRemotePortBusy || k == KindLocalPortBusy
}

type LineInfo struct {
	Up            bool
	Authenticated bool
	Failure       Kind
	Debug         bool
	Fingerprint   string
}

type Options struct {
	Exe                 string
	KnownHostsFile      string
	StrictHostKeyCheck  string // OpenSSH: "yes" | "ask" | "accept-new"
	ServerAliveInterval int
	ServerAliveCountMax int
	AgentPipe           string // OpenSSH agent named pipe (\\.\pipe\... form)
	AskpassEnv          []string
	PwFile              string // plink password file
	PlinkSession        string // saved PuTTY session with keepalive
}

type Command struct {
	Exe  string
	Args []string
	Env  []string
}

type Backend interface {
	Command(t config.Tunnel, o Options) Command
	ClassifyLine(line string) LineInfo
}

func For(b config.Backend) Backend {
	if b == config.BackendPlink {
		return Plink{}
	}
	return OpenSSH{}
}

func hostPort(addr string, port int) string {
	if ip := net.ParseIP(addr); ip != nil && ip.To4() == nil {
		return "[" + addr + "]:" + strconv.Itoa(port)
	}
	return addr + ":" + strconv.Itoa(port)
}

// forwardSpec renders bind:port:target:port for -R / -L.
func forwardSpec(f config.Forward) (flag, spec string) {
	flag = "-R"
	if f.Type == config.ForwardLocal {
		flag = "-L"
	}
	return flag, hostPort(f.BindAddress, f.BindPort) + ":" + hostPort(f.TargetHost, f.TargetPort)
}
