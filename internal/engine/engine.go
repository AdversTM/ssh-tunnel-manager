// Package engine prepares each connection attempt: askpass wiring for ssh.exe,
// password files for plink, agent pipes and the PuTTY keepalive session.
package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"ssh-tunnel-manager/internal/askpass"
	"ssh-tunnel-manager/internal/backend"
	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/hostkey"
	"ssh-tunnel-manager/internal/pwfile"
	"ssh-tunnel-manager/internal/secrets"
)

const (
	DefaultSSHExe       = `C:\Windows\System32\OpenSSH\ssh.exe`
	DefaultPlinkExe     = `C:\Program Files\PuTTY\plink.exe`
	DefaultPlinkSession = "ssh-tunnel-manager"
	openSSHAgentPipe    = `\\.\pipe\openssh-ssh-agent`
)

type HostKeyRequest struct {
	TunnelID    string
	TunnelName  string
	Host        string
	KeyType     string
	Fingerprint string
}

type Engine struct {
	SSHExe    string
	PlinkExe  string
	DataDir   string
	HelperExe string

	Secrets secrets.Store
	Askpass *askpass.Server

	// HostKeyPrompt asks the user to accept an unknown OpenSSH host key; blocks until answered.
	HostKeyPrompt func(HostKeyRequest) bool
	Log           func(tunnelID, text string)

	ServerAliveInterval int
	ServerAliveCountMax int
	PlinkSession        string
}

func (e *Engine) KnownHostsFile() string { return filepath.Join(e.DataDir, "known_hosts") }
func (e *Engine) TmpDir() string         { return filepath.Join(e.DataDir, "tmp") }

func (e *Engine) log(id, text string) {
	if e.Log != nil {
		e.Log(id, text)
	}
}

func CredentialRef(t config.Tunnel) string {
	if t.Auth.CredentialRef != "" {
		return t.Auth.CredentialRef
	}
	return "ssh-tunnel-manager:" + t.ID
}

func (e *Engine) secret(t config.Tunnel) (string, error) {
	s, err := e.Secrets.Get(CredentialRef(t))
	if errors.Is(err, secrets.ErrNotFound) {
		return "", fmt.Errorf("no password/passphrase stored for tunnel %q", t.Name)
	}
	return s, err
}

func isPasswordPrompt(p string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(p)), "password:")
}

// Responder answers ssh.exe prompts for one tunnel. A prompt that does not match
// the tunnel's auth method is refused, so e.g. a key passphrase is never sent
// to the server as a login password.
func (e *Engine) Responder(t config.Tunnel) askpass.Responder {
	return func(p string) (string, error) {
		switch {
		case hostkey.IsPrompt(p):
			info, ok := hostkey.ParsePrompt(p)
			if !ok {
				return "", errors.New("unparsable host key prompt")
			}
			req := HostKeyRequest{TunnelID: t.ID, TunnelName: t.Name, Host: info.Host, KeyType: info.KeyType, Fingerprint: info.Fingerprint}
			if e.HostKeyPrompt != nil && e.HostKeyPrompt(req) {
				e.log(t.ID, "host key accepted by user: "+info.Fingerprint)
				return "yes", nil
			}
			e.log(t.ID, "host key rejected by user: "+info.Fingerprint)
			return "no", nil
		case strings.Contains(p, "Enter passphrase for key"):
			if t.Auth.Method != config.AuthKey {
				e.log(t.ID, "askpass: refused passphrase prompt (auth method is "+string(t.Auth.Method)+")")
				return "", errors.New("passphrase prompt not expected")
			}
			return e.answerSecret(t)
		case isPasswordPrompt(p):
			if t.Auth.Method != config.AuthPassword {
				e.log(t.ID, "askpass: refused password prompt (auth method is "+string(t.Auth.Method)+")")
				return "", errors.New("password prompt not expected")
			}
			return e.answerSecret(t)
		}
		first, _, _ := strings.Cut(p, "\n")
		e.log(t.ID, "askpass: refused unexpected prompt: "+first)
		return "", errors.New("unexpected prompt")
	}
}

func (e *Engine) answerSecret(t config.Tunnel) (string, error) {
	s, err := e.secret(t)
	if err != nil {
		e.log(t.ID, "askpass: "+err.Error())
	}
	return s, err
}

// Prepare implements supervisor.PrepareFunc.
func (e *Engine) Prepare(t config.Tunnel) (backend.Options, func(), error) {
	o := backend.Options{
		KnownHostsFile:      e.KnownHostsFile(),
		ServerAliveInterval: e.ServerAliveInterval,
		ServerAliveCountMax: e.ServerAliveCountMax,
		PlinkSession:        e.PlinkSession,
	}
	noop := func() {}
	switch t.Backend {
	case config.BackendPlink:
		o.Exe = e.PlinkExe
		if t.Auth.Method == config.AuthPassword {
			pw, err := e.secret(t)
			if err != nil {
				return o, nil, err
			}
			path, remove, err := pwfile.Create(e.TmpDir(), pw)
			if err != nil {
				return o, nil, err
			}
			o.PwFile = path
			return o, remove, nil
		}
		return o, noop, nil
	default:
		o.Exe = e.SSHExe
		o.StrictHostKeyCheck = "ask"
		if t.Auth.Method == config.AuthAgent {
			pipe, err := AgentPipe(t.Auth.Agent)
			if err != nil {
				return o, nil, err
			}
			o.AgentPipe = pipe
		}
		token, revoke, err := e.Askpass.Register(e.Responder(t))
		if err != nil {
			return o, nil, err
		}
		o.AskpassEnv = e.Askpass.Env(token, e.HelperExe)
		return o, revoke, nil
	}
}

func listPipes() ([]string, error) {
	entries, err := os.ReadDir(`\\.\pipe\`)
	if err != nil {
		return nil, fmt.Errorf("list named pipes: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		names = append(names, en.Name())
	}
	return names, nil
}

// AgentPipe finds the named pipe of the selected agent.
func AgentPipe(a config.Agent) (string, error) {
	names, err := listPipes()
	if err != nil {
		return "", err
	}
	switch a {
	case config.AgentOpenSSHAgent:
		for _, n := range names {
			if n == "openssh-ssh-agent" {
				return openSSHAgentPipe, nil
			}
		}
		return "", errors.New(`Windows ssh-agent is not running (as admin: Set-Service ssh-agent -StartupType Automatic; Start-Service ssh-agent)`)
	default:
		for _, n := range names {
			if strings.HasPrefix(n, "pageant.") {
				return `\\.\pipe\` + n, nil
			}
		}
		return "", errors.New("Pageant is not running")
	}
}

// EnsurePlinkSession creates/updates the saved PuTTY session that plink loads
// with -load to get SSH keepalives (plink has no keepalive CLI flag).
func EnsurePlinkSession(name string, pingSeconds int) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\SimonTatham\PuTTY\Sessions\`+name, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create PuTTY session %s: %w", name, err)
	}
	defer k.Close()
	if err := k.SetDWordValue("PingIntervalSecs", uint32(pingSeconds)); err != nil {
		return fmt.Errorf("set PingIntervalSecs: %w", err)
	}
	return nil
}

// ForgetOpenSSHHostKey removes the stored key so the next connect prompts again
// (used when the user accepts a changed host key).
func (e *Engine) ForgetOpenSSHHostKey(t config.Tunnel) error {
	return hostkey.Remove(e.SSHExe, e.KnownHostsFile(), t.Host, t.Port)
}

func (e *Engine) KnownOpenSSHFingerprint(t config.Tunnel) (string, error) {
	return hostkey.KnownFingerprint(e.SSHExe, e.KnownHostsFile(), t.Host, t.Port)
}
