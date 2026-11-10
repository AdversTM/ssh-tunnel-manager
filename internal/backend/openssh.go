package backend

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"ssh-tunnel-manager/internal/config"
)

type OpenSSH struct{}

// sshPath renders a path for ssh_config option values: forward slashes avoid
// backslash escaping, quotes keep paths with spaces as one value.
func sshPath(p string) string {
	return `"` + filepath.ToSlash(p) + `"`
}

func (OpenSSH) Command(t config.Tunnel, o Options) Command {
	strict := o.StrictHostKeyCheck
	if strict == "" {
		strict = "yes"
	}
	batch := "yes"
	if len(o.AskpassEnv) > 0 {
		batch = "no"
	}
	args := []string{
		"-F", "NUL", "-N", "-T", "-v",
		"-p", strconv.Itoa(t.Port), "-l", t.User,
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=" + strconv.Itoa(o.ServerAliveInterval),
		"-o", "ServerAliveCountMax=" + strconv.Itoa(o.ServerAliveCountMax),
		"-o", "StrictHostKeyChecking=" + strict,
		"-o", "UserKnownHostsFile=" + sshPath(o.KnownHostsFile),
		"-o", "NumberOfPasswordPrompts=1",
		"-o", "BatchMode=" + batch,
	}
	switch t.Auth.Method {
	case config.AuthKey:
		args = append(args,
			"-o", "PreferredAuthentications=publickey",
			"-o", "IdentitiesOnly=yes",
			"-o", "IdentityAgent=none",
			"-i", t.Auth.KeyPath)
	case config.AuthAgent:
		args = append(args,
			"-o", "PreferredAuthentications=publickey",
			// Win32-OpenSSH accepts the agent pipe only with forward slashes.
			"-o", "IdentityAgent="+strings.ReplaceAll(o.AgentPipe, `\`, "/"))
	case config.AuthPassword:
		args = append(args,
			"-o", "PreferredAuthentications=password,keyboard-interactive",
			"-o", "PubkeyAuthentication=no")
	}
	flag, spec := forwardSpec(t.Forward)
	args = append(args, flag, spec, t.Host)
	return Command{Exe: o.Exe, Args: args, Env: o.AskpassEnv}
}

var sha256Line = regexp.MustCompile(`^SHA256:([A-Za-z0-9+/=]+)\.?$`)

func (OpenSSH) ClassifyLine(line string) LineInfo {
	info := LineInfo{Debug: strings.HasPrefix(line, "debug")}
	has := func(s string) bool { return strings.Contains(line, s) }
	switch {
	case has("remote forward success for: listen"), has("Local forwarding listening on"):
		info.Up, info.Debug = true, false
	case strings.HasPrefix(line, "Authenticated to "):
		info.Authenticated = true
	case has("remote port forwarding failed for listen port"):
		info.Failure = KindRemotePortBusy
	case has("cannot listen to port"), has("Could not request local forwarding"),
		has("Address already in use"):
		info.Failure = KindLocalPortBusy
	case has("REMOTE HOST IDENTIFICATION HAS CHANGED"),
		has("has changed and you have requested strict checking"):
		info.Failure = KindHostKeyChanged
	case has("host key is known for") && has("requested strict checking"),
		line == "Host key verification failed.":
		info.Failure = KindHostKeyUnknown
	case has("Permission denied ("), has("Too many authentication failures"),
		has("incorrect passphrase supplied"):
		info.Failure = KindAuth
	case has("UNPROTECTED PRIVATE KEY FILE"), has("bad permissions"),
		has("no such identity"), has("Bad configuration option"),
		has("Load key") && has("No such file"):
		info.Failure = KindConfig
	case has("Connection timed out"), has("Connection refused"),
		has("Timeout, server"), has("closed by remote host"),
		has("Connection reset"), has("Broken pipe"),
		has("Could not resolve hostname"), has("Network is unreachable"),
		has("No route to host"), has("Connection closed by"),
		has("client_loop: send disconnect"), has("kex_exchange_identification"):
		info.Failure = KindNetwork
	}
	if m := sha256Line.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
		info.Fingerprint = "SHA256:" + m[1]
	}
	return info
}
