package askpass

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"ssh-tunnel-manager/internal/pwfile"
)

func TestMain(m *testing.M) {
	if IsHelperInvocation() {
		os.Exit(RunHelper(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func runHelper(t *testing.T, env []string, prompt string) (string, int) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, prompt)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), code
}

func TestHelperRoundTrip(t *testing.T) {
	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var seen string
	tok, revoke, err := s.Register(func(p string) (string, error) {
		seen = p
		return "s3cret", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	env := s.Env(tok, self)

	out, code := runHelper(t, env, "pwuser@host's password: ")
	if code != 0 || strings.TrimRight(out, "\r\n") != "s3cret" {
		t.Fatalf("out=%q code=%d", out, code)
	}
	if seen != "pwuser@host's password: " {
		t.Fatalf("prompt seen %q", seen)
	}

	revoke()
	out, code = runHelper(t, env, "x")
	if code == 0 || out != "" {
		t.Fatalf("revoked token answered: out=%q code=%d", out, code)
	}
}

func TestHelperUnknownTokenAndResponderError(t *testing.T) {
	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	self, _ := os.Executable()
	if out, code := runHelper(t, s.Env("deadbeef", self), "x"); code == 0 || out != "" {
		t.Fatalf("unknown token: out=%q code=%d", out, code)
	}
	tok, _, err := s.Register(func(string) (string, error) { return "", errors.New("unexpected prompt") })
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runHelper(t, s.Env(tok, self), "x"); code == 0 || out != "" {
		t.Fatalf("refused prompt: out=%q code=%d", out, code)
	}
}

func TestPipeIsOwnerOnly(t *testing.T) {
	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var sd *windows.SECURITY_DESCRIPTOR
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		sd, err = windows.GetNamedSecurityInfo(s.PipeName(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	wantSDDL, err := pwfile.OwnerOnlySDDL()
	if err != nil {
		t.Fatal(err)
	}
	wantSD, err := windows.SecurityDescriptorFromString(wantSDDL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sd.String(), wantSD.String(); got != want {
		t.Fatalf("pipe dacl %s want %s", got, want)
	}
}
