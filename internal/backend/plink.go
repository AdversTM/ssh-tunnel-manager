package backend

import (
	"regexp"
	"strconv"
	"strings"

	"ssh-tunnel-manager/internal/config"
)

type Plink struct{}

func (Plink) Command(t config.Tunnel, o Options) Command {
	var args []string
	if o.PlinkSession != "" {
		args = append(args, "-load", o.PlinkSession)
	}
	args = append(args, "-ssh", "-batch", "-N", "-v",
		"-P", strconv.Itoa(t.Port), "-l", t.User)
	if t.HostKey.PlinkFingerprint != "" {
		args = append(args, "-hostkey", t.HostKey.PlinkFingerprint)
	}
	switch t.Auth.Method {
	case config.AuthKey:
		args = append(args, "-noagent", "-i", t.Auth.KeyPath)
	case config.AuthAgent:
		args = append(args, "-agent")
	case config.AuthPassword:
		args = append(args, "-noagent", "-pwfile", o.PwFile)
	}
	flag, spec := forwardSpec(t.Forward)
	args = append(args, flag, spec, t.Host)
	return Command{Exe: o.Exe, Args: args}
}

var (
	plinkFingerprint = regexp.MustCompile(`^\S+ \d+ (SHA256:[A-Za-z0-9+/=]+)$`)
	plinkNoise       = []string{
		"Looking up host", "Connecting to ", "Connected to ", "We claim version",
		"Remote version", "Using SSH protocol", "No GSSAPI", "Doing ", "Initialised ",
		"Host key fingerprint is:", "Using username", "Sent password", "Sent public key",
		"Trying Pageant key", "Pageant is running", "Offered public key", "Authenticating with public key",
		"Requesting remote port", "Requesting local port", "Opening connection to", "Forwarded connection",
	}
)

func (Plink) ClassifyLine(line string) LineInfo {
	var info LineInfo
	trimmed := strings.TrimSpace(line)
	has := func(s string) bool { return strings.Contains(line, s) }
	for _, n := range plinkNoise {
		if strings.HasPrefix(trimmed, n) {
			info.Debug = true
			break
		}
	}
	switch {
	case trimmed == "Access granted":
		info.Authenticated = true
	case strings.HasPrefix(trimmed, "Remote port forwarding from") && strings.HasSuffix(trimmed, "enabled"),
		strings.HasPrefix(trimmed, "Local port") && has("forwarding to") && !has("failed"):
		info.Up = true
	case strings.HasPrefix(trimmed, "Remote port forwarding from") && has("refused"):
		info.Failure = KindRemotePortBusy
	case has("Address already in use"), strings.HasPrefix(trimmed, "Local port") && has("failed"):
		info.Failure = KindLocalPortBusy
	case has("Host key not in manually configured list"), has("POTENTIAL SECURITY BREACH"):
		info.Failure = KindHostKeyChanged
	case has("Cannot confirm a host key in batch mode"):
		info.Failure = KindHostKeyUnknown
	case has("Configured password was not accepted"), has("No supported authentication methods available"),
		has("Cannot answer interactive prompts in batch mode"), has("Server refused our key"),
		trimmed == "Access denied":
		info.Failure = KindAuth
	case has("Unable to open"), has("Unable to load key file"), has("Unable to use key file"),
		has("unable to open password file"):
		info.Failure = KindConfig
	case has("Network error"), has("unexpectedly closed network connection"),
		has("Connection refused"), has("timed out"), has("Host does not exist"):
		info.Failure = KindNetwork
	}
	if m := plinkFingerprint.FindStringSubmatch(trimmed); m != nil {
		info.Fingerprint = m[1]
		info.Debug = true
	}
	return info
}
