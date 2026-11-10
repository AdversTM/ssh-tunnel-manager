// Package serverkit generates the server-side setup for a tunnel: instructions,
// an idempotent bash script (sshd modes) and a Caddy site block. The app only
// shows/saves these; it never runs anything on the server.
package serverkit

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"ssh-tunnel-manager/internal/config"
)

type Kit struct {
	Mode         config.ServerMode `json:"mode"`
	Instructions string            `json:"instructions"`
	Script       string            `json:"script"`
	ScriptName   string            `json:"scriptName"`
	Caddyfile    string            `json:"caddyfile"`
}

//go:embed script.sh.tmpl
var scriptSource string

var script = template.Must(template.New("script").Funcs(template.FuncMap{"q": shellQuote}).Parse(scriptSource))

var (
	userRe   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	domainRe = regexp.MustCompile(`^(?i)[a-z0-9*]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	pubKeyRe = regexp.MustCompile(`^((?:ssh|ecdsa|sk)-[A-Za-z0-9@.-]+) ([A-Za-z0-9+/]+={0,2})(?: .*)?$`)
	slugRe   = regexp.MustCompile(`[^a-z0-9]+`)
)

func shellQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

func hostPort(host string, port int) string {
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(port)
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NormalizePublicKey returns "type base64" from an OpenSSH public key line.
func NormalizePublicKey(s string) (string, error) {
	m := pubKeyRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", errors.New("not an OpenSSH public key (expected e.g. \"ssh-ed25519 AAAA...\")")
	}
	return m[1] + " " + m[2], nil
}

// PublicKey reads the public half of a private key file: <key>.pub next to an
// OpenSSH key, or the public lines inside a PuTTY .ppk.
func PublicKey(keyPath string) (string, error) {
	if strings.EqualFold(filepath.Ext(keyPath), ".ppk") {
		return ppkPublicKey(keyPath)
	}
	b, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return "", err
	}
	return NormalizePublicKey(string(b))
}

func ppkPublicKey(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var keyType string
	var blob strings.Builder
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ": ")
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(k, "PuTTY-User-Key-File-"):
			keyType = v
		case k == "Public-Lines":
			n, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("%s: bad Public-Lines", path)
			}
			for i := 0; i < n && sc.Scan(); i++ {
				blob.WriteString(strings.TrimSpace(sc.Text()))
			}
			return NormalizePublicKey(keyType + " " + blob.String())
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s: no public key found", path)
}

type scriptData struct {
	Name, Mode, Host, ScriptName                      string
	User, Keyword, Spec, AuthorizedKeysOption, PubKey string
	Domain, Upstream                                  string
}

// Generate builds the kit for t. pubKey ("type base64 [comment]") goes into
// authorized_keys; empty leaves that step to the user (agent/password auth).
func Generate(t config.Tunnel, pubKey string) (Kit, error) {
	mode := t.Server.Mode
	if mode == "" {
		mode = config.ServerNone
	}
	f := t.Forward
	listen := hostPort(f.BindAddress, f.BindPort)
	target := hostPort(f.TargetHost, f.TargetPort)
	kit := Kit{Mode: mode}

	if mode == config.ServerNone {
		kit.Instructions = noneInstructions(t, listen, target)
		return kit, nil
	}

	if !userRe.MatchString(t.User) {
		return Kit{}, fmt.Errorf("user %q is not a valid Linux user name for the setup script", t.User)
	}
	if t.User == "root" {
		return Kit{}, errors.New("the setup script needs a dedicated tunnel user, not root")
	}
	if pubKey != "" {
		k, err := NormalizePublicKey(pubKey)
		if err != nil {
			return Kit{}, err
		}
		pubKey = k
	}
	d := scriptData{Name: t.Name, Mode: string(mode), Host: t.Host, User: t.User, PubKey: pubKey}
	if f.Type == config.ForwardRemote {
		d.Keyword, d.Spec = "PermitListen", listen
		d.AuthorizedKeysOption = `permitlisten="` + listen + `"`
	} else {
		d.Keyword, d.Spec = "PermitOpen", target
		d.AuthorizedKeysOption = `permitopen="` + target + `"`
	}
	if mode == config.ServerSSHDCaddy {
		if f.Type != config.ForwardRemote {
			return Kit{}, errors.New("sshd+caddy mode needs a remote (-R) forward: Caddy proxies to the port the tunnel opens on the server")
		}
		d.Domain = strings.TrimSpace(t.Server.Domain)
		if !domainRe.MatchString(d.Domain) {
			return Kit{}, fmt.Errorf("domain %q is not a valid host name", d.Domain)
		}
		upHost := f.BindAddress
		if net.ParseIP(upHost).IsUnspecified() {
			upHost = "127.0.0.1"
		}
		d.Upstream = hostPort(upHost, f.BindPort)
		kit.Caddyfile = fmt.Sprintf("%s {\n\treverse_proxy %s\n}\n", d.Domain, d.Upstream)
	}
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(t.Name), "-"), "-")
	if slug == "" {
		slug = "tunnel"
	}
	d.ScriptName = "stm-setup-" + slug + ".sh"
	var b strings.Builder
	if err := script.Execute(&b, d); err != nil {
		return Kit{}, err
	}
	kit.Script, kit.ScriptName = b.String(), d.ScriptName
	kit.Instructions = sshdInstructions(t, d)
	return kit, nil
}

func noneInstructions(t config.Tunnel, listen, target string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Mode: none - nothing is changed on %s.\n\n", t.Host)
	if t.Forward.Type == config.ForwardRemote {
		fmt.Fprintf(&b, "The server will listen on %s and forward connections to %s as reached from this PC.\n", listen, target)
		if !loopback(t.Forward.BindAddress) {
			fmt.Fprintf(&b, "\nWarning: bind address %s needs \"GatewayPorts clientspecified\" on the server. "+
				"With the default (GatewayPorts no) sshd binds to 127.0.0.1 only. Switch to mode \"sshd\" to generate that setup.\n",
				t.Forward.BindAddress)
		}
	} else {
		fmt.Fprintf(&b, "This PC will listen on %s and forward connections to %s as reached from the server.\n", listen, target)
	}
	fmt.Fprintf(&b, "\nRequirements: user %q can log in with the configured %s authentication and AllowTcpForwarding is not disabled for it.\n",
		t.User, t.Auth.Method)
	return b.String()
}

func sshdInstructions(t config.Tunnel, d scriptData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Mode: %s - run the script once on %s as root. It is safe to run again.\n\n", d.Mode, t.Host)
	fmt.Fprintf(&b, "1. Copy %s to the server, e.g.:\n   scp %s <admin>@%s:\n", d.ScriptName, d.ScriptName, t.Host)
	fmt.Fprintf(&b, "2. Run it:  sudo bash %s\n", d.ScriptName)
	fmt.Fprintf(&b, "   It creates user %q (no shell), allows only %s %s for it in /etc/ssh/sshd_config.d/ssh-tunnel-manager-%s.conf, "+
		"sets ClientAliveInterval 15 / ClientAliveCountMax 3 so a dead session frees the port within ~45 s, checks sshd -t and reloads sshd.\n",
		t.User, d.Keyword, d.Spec, t.User)
	switch {
	case d.PubKey != "":
		fmt.Fprintf(&b, "   Your public key is added to ~%s/.ssh/authorized_keys restricted to %s.\n", t.User, d.AuthorizedKeysOption)
	case t.Auth.Method == config.AuthPassword:
		fmt.Fprintf(&b, "3. Set a password for the user (password login must be allowed by sshd):  sudo passwd %s\n", t.User)
	default:
		fmt.Fprintf(&b, "3. No public key was available: edit the PUBKEY= line in the script (e.g. PUBKEY='ssh-ed25519 AAAA...') before running it, "+
			"or add the key to ~%s/.ssh/authorized_keys yourself.\n", t.User)
	}
	if d.Domain != "" {
		fmt.Fprintf(&b, "\nCaddy: point DNS for %s at the server and open ports 80/443. If Caddy is installed (/etc/caddy/Caddyfile) the script "+
			"appends the site block below, validates and reloads it; otherwise it prints the block for you to add:\n\n%s {\n\treverse_proxy %s\n}\n",
			d.Domain, d.Domain, d.Upstream)
	}
	fmt.Fprintf(&b, "\nCheck after connecting the tunnel:  sudo ss -ltnp | grep ':%d '\n", t.Forward.BindPort)
	if t.Forward.Type == config.ForwardLocal {
		fmt.Fprintf(&b, "(For a -L tunnel the server opens no port; the tunnel is allowed to reach only %s.)\n", d.Spec)
	}
	return b.String()
}
