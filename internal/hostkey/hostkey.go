// Package hostkey handles OpenSSH known_hosts entries owned by the app and
// parses ssh host key confirmation prompts.
package hostkey

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

type PromptInfo struct {
	Host        string
	KeyType     string
	Fingerprint string
}

var (
	promptHost = regexp.MustCompile(`authenticity of host '([^' ]+)`)
	promptKey  = regexp.MustCompile(`(\S+) key fingerprint is (SHA256:[A-Za-z0-9+/=]+)`)
	sha256Tok  = regexp.MustCompile(`SHA256:[A-Za-z0-9+/=]+`)
)

// IsPrompt reports whether an askpass prompt asks to confirm a host key.
func IsPrompt(p string) bool {
	return strings.Contains(p, "authenticity of host") && strings.Contains(p, "(yes/no")
}

func ParsePrompt(p string) (PromptInfo, bool) {
	h := promptHost.FindStringSubmatch(p)
	k := promptKey.FindStringSubmatch(p)
	if h == nil || k == nil {
		return PromptInfo{}, false
	}
	return PromptInfo{Host: h[1], KeyType: k[1], Fingerprint: k[2]}, true
}

// KnownHostName is the known_hosts name OpenSSH uses for host:port.
func KnownHostName(host string, port int) string {
	if port == 22 {
		return host
	}
	return "[" + host + "]:" + strconv.Itoa(port)
}

func keygen(sshExe string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(filepath.Dir(sshExe), "ssh-keygen.exe"), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// KnownFingerprint returns the stored SHA256 fingerprint for host:port, or "" if none.
func KnownFingerprint(sshExe, knownHosts, host string, port int) (string, error) {
	if _, err := os.Stat(knownHosts); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	out, err := keygen(sshExe, "-l", "-F", KnownHostName(host, port), "-f", knownHosts)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("ssh-keygen -F: %w: %s", err, out)
	}
	return sha256Tok.FindString(out), nil
}

// Remove deletes all entries for host:port (used when the user accepts a changed key).
func Remove(sshExe, knownHosts, host string, port int) error {
	if _, err := os.Stat(knownHosts); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if out, err := keygen(sshExe, "-R", KnownHostName(host, port), "-f", knownHosts); err != nil {
		return fmt.Errorf("ssh-keygen -R: %w: %s", err, out)
	}
	if err := os.Remove(knownHosts + ".old"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove known_hosts backup: %w", err)
	}
	return nil
}
