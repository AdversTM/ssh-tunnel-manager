package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const CurrentVersion = 1

type Backend string

const (
	BackendOpenSSH Backend = "openssh"
	BackendPlink   Backend = "plink"
)

type AuthMethod string

const (
	AuthKey      AuthMethod = "key"
	AuthAgent    AuthMethod = "agent"
	AuthPassword AuthMethod = "password"
)

type Agent string

const (
	AgentPageant      Agent = "pageant"
	AgentOpenSSHAgent Agent = "openssh-agent"
)

type ForwardType string

const (
	ForwardRemote ForwardType = "R"
	ForwardLocal  ForwardType = "L"
)

type ServerMode string

const (
	ServerNone      ServerMode = "none"
	ServerSSHD      ServerMode = "sshd"
	ServerSSHDCaddy ServerMode = "sshd+caddy"
)

type Auth struct {
	Method        AuthMethod `json:"method"`
	KeyPath       string     `json:"keyPath,omitempty"`
	Agent         Agent      `json:"agent,omitempty"`
	CredentialRef string     `json:"credentialRef,omitempty"`
}

type Forward struct {
	Type        ForwardType `json:"type"`
	BindAddress string      `json:"bindAddress"`
	BindPort    int         `json:"bindPort"`
	TargetHost  string      `json:"targetHost"`
	TargetPort  int         `json:"targetPort"`
}

type HostKey struct {
	PlinkFingerprint string `json:"plinkFingerprint,omitempty"`
}

type Server struct {
	Mode   ServerMode `json:"mode"`
	Domain string     `json:"domain,omitempty"`
}

type Tunnel struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Backend     Backend `json:"backend"`
	Host        string  `json:"host"`
	Port        int     `json:"port"`
	User        string  `json:"user"`
	Auth        Auth    `json:"auth"`
	Forward     Forward `json:"forward"`
	AutoConnect bool    `json:"autoConnect"`
	HostKey     HostKey `json:"hostKey"`
	Server      Server  `json:"server"`
}

type Settings struct {
	Autostart     bool   `json:"autostart"`
	Notifications bool   `json:"notifications"`
	SSHPath       string `json:"sshPath,omitempty"`
	PlinkPath     string `json:"plinkPath,omitempty"`
}

type Config struct {
	Version  int      `json:"version"`
	Settings Settings `json:"settings"`
	Tunnels  []Tunnel `json:"tunnels"`
}

// ErrCorrupt is returned by Load when the file existed but could not be parsed;
// the returned Config is a fresh default and the broken file was moved aside.
var ErrCorrupt = errors.New("config file corrupt")

func Default() Config {
	return Config{
		Version:  CurrentVersion,
		Settings: Settings{Notifications: true},
		Tunnels:  []Tunnel{},
	}
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

func validBindAddress(a string) bool {
	return a == "localhost" || net.ParseIP(a) != nil
}

func (t Tunnel) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if strings.TrimSpace(t.ID) == "" {
		add("id is required")
	}
	if strings.TrimSpace(t.Name) == "" {
		add("name is required")
	}
	if t.Backend != BackendOpenSSH && t.Backend != BackendPlink {
		add("backend must be %q or %q", BackendOpenSSH, BackendPlink)
	}
	if strings.TrimSpace(t.Host) == "" {
		add("host is required")
	}
	if !validPort(t.Port) {
		add("port must be 1-65535")
	}
	if strings.TrimSpace(t.User) == "" {
		add("user is required")
	}

	switch t.Auth.Method {
	case AuthKey:
		if t.Auth.KeyPath == "" {
			add("key path is required for key auth")
		} else if _, err := os.Stat(t.Auth.KeyPath); err != nil {
			add("key file not accessible: %v", err)
		}
	case AuthAgent:
		switch t.Auth.Agent {
		case AgentPageant:
		case AgentOpenSSHAgent:
			if t.Backend == BackendPlink {
				add("plink supports only the pageant agent")
			}
		default:
			add("agent must be %q or %q", AgentPageant, AgentOpenSSHAgent)
		}
	case AuthPassword:
	default:
		add("auth method must be key, agent or password")
	}

	f := t.Forward
	if f.Type != ForwardRemote && f.Type != ForwardLocal {
		add("forward type must be R or L")
	}
	if !validBindAddress(f.BindAddress) {
		add("bind address must be an IP address or localhost")
	}
	if !validPort(f.BindPort) {
		add("bind port must be 1-65535")
	}
	if strings.TrimSpace(f.TargetHost) == "" {
		add("target host is required")
	}
	if !validPort(f.TargetPort) {
		add("target port must be 1-65535")
	}

	switch t.Server.Mode {
	case "", ServerNone, ServerSSHD:
	case ServerSSHDCaddy:
		if strings.TrimSpace(t.Server.Domain) == "" {
			add("domain is required for sshd+caddy mode")
		}
	default:
		add("server mode must be none, sshd or sshd+caddy")
	}
	return errors.Join(errs...)
}

func (c Config) Validate() error {
	var errs []error
	seen := map[string]bool{}
	for i, t := range c.Tunnels {
		if err := t.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("tunnel %d (%s): %w", i, t.Name, err))
		}
		if seen[t.ID] {
			errs = append(errs, fmt.Errorf("tunnel %d (%s): duplicate id %q", i, t.Name, t.ID))
		}
		seen[t.ID] = true
	}
	return errors.Join(errs...)
}

// Load reads the config. A missing file yields Default(). An unparsable file is
// renamed to <path>.bak-<timestamp> and Default() is returned with ErrCorrupt.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		backup := fmt.Sprintf("%s.bak-%s", path, time.Now().Format("20060102-150405"))
		if rerr := os.Rename(path, backup); rerr != nil {
			return Default(), fmt.Errorf("%w: %v (backup failed: %v)", ErrCorrupt, err, rerr)
		}
		return Default(), fmt.Errorf("%w: %v (moved to %s)", ErrCorrupt, err, backup)
	}
	if cfg.Tunnels == nil {
		cfg.Tunnels = []Tunnel{}
	}
	return cfg, nil
}

// Save writes atomically: temp file in the same directory, then rename.
func Save(path string, cfg Config) error {
	cfg.Version = CurrentVersion
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return errors.Join(fmt.Errorf("write temp config: %w", err), os.Remove(tmpName))
	}
	if err := os.Rename(tmpName, path); err != nil {
		return errors.Join(fmt.Errorf("replace config: %w", err), os.Remove(tmpName))
	}
	return nil
}
