// Package askpass answers ssh.exe SSH_ASKPASS prompts. ssh.exe runs our own exe
// in helper mode; the helper forwards the prompt over a named pipe (current user
// only) to the app, which answers from Credential Manager or the GUI. Secrets
// never appear in arguments or environment variables; each connection attempt
// gets a one-time token.
package askpass

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"

	"ssh-tunnel-manager/internal/pwfile"
)

const (
	EnvPipe  = "STM_ASKPASS_PIPE"
	EnvToken = "STM_ASKPASS_TOKEN"
)

// Responder answers one prompt; returning an error makes ssh treat it as cancelled.
type Responder func(prompt string) (string, error)

type request struct {
	Token  string `json:"token"`
	Prompt string `json:"prompt"`
}

type response struct {
	Answer string `json:"answer,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Server struct {
	name string
	ln   net.Listener

	mu     sync.Mutex
	tokens map[string]Responder
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func NewServer() (*Server, error) {
	suffix, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	sddl, err := pwfile.OwnerOnlySDDL()
	if err != nil {
		return nil, err
	}
	name := `\\.\pipe\ssh-tunnel-manager-askpass-` + suffix
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", name, err)
	}
	s := &Server{name: name, ln: ln, tokens: map[string]Responder{}}
	go s.serve()
	return s, nil
}

func (s *Server) PipeName() string { return s.name }

func (s *Server) Close() error { return s.ln.Close() }

// Register returns a one-time token for one connection attempt and a revoke func.
func (s *Server) Register(r Responder) (token string, revoke func(), err error) {
	token, err = randomHex(32)
	if err != nil {
		return "", nil, err
	}
	s.mu.Lock()
	s.tokens[token] = r
	s.mu.Unlock()
	return token, func() {
		s.mu.Lock()
		delete(s.tokens, token)
		s.mu.Unlock()
	}, nil
}

// Env returns the environment for ssh.exe so it calls helperExe as askpass.
func (s *Server) Env(token, helperExe string) []string {
	return []string{
		"SSH_ASKPASS=" + helperExe,
		"SSH_ASKPASS_REQUIRE=force",
		EnvPipe + "=" + s.name,
		EnvToken + "=" + token,
	}
}

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if errors.Is(err, winio.ErrPipeListenerClosed) || errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			continue
		}
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return
	}
	var req request
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&req); err != nil {
		return
	}
	s.mu.Lock()
	r, ok := s.tokens[req.Token]
	s.mu.Unlock()
	var resp response
	if !ok {
		resp.Error = "unknown token"
	} else if answer, err := r(req.Prompt); err != nil {
		resp.Error = err.Error()
	} else {
		resp.Answer = answer
	}
	if err := json.NewEncoder(c).Encode(resp); err != nil {
		return
	}
}

// IsHelperInvocation reports whether the process was started by ssh.exe as askpass.
func IsHelperInvocation() bool { return os.Getenv(EnvPipe) != "" }

// RunHelper implements helper mode: prompt in args, answer on stdout.
// Returns the process exit code.
func RunHelper(args []string, stdout, stderr io.Writer) int {
	timeout := 5 * time.Second
	c, err := winio.DialPipe(os.Getenv(EnvPipe), &timeout)
	if err != nil {
		fmt.Fprintln(stderr, "askpass: connect:", err)
		return 1
	}
	defer c.Close()
	req := request{Token: os.Getenv(EnvToken), Prompt: strings.Join(args, " ")}
	if err := json.NewEncoder(c).Encode(req); err != nil {
		fmt.Fprintln(stderr, "askpass: send:", err)
		return 1
	}
	var resp response
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		fmt.Fprintln(stderr, "askpass: receive:", err)
		return 1
	}
	if resp.Error != "" {
		fmt.Fprintln(stderr, "askpass:", resp.Error)
		return 1
	}
	if _, err := fmt.Fprintln(stdout, resp.Answer); err != nil {
		return 1
	}
	return 0
}
