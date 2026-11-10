// Package manager is the application core behind the GUI and tray: tunnel
// configuration and secrets, the supervisor, per-tunnel logs and the host key
// confirmation flow. It has no Wails dependency so it can be tested directly.
package manager

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"ssh-tunnel-manager/internal/askpass"
	"ssh-tunnel-manager/internal/backend"
	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/engine"
	"ssh-tunnel-manager/internal/hostkey"
	"ssh-tunnel-manager/internal/logs"
	"ssh-tunnel-manager/internal/pwfile"
	"ssh-tunnel-manager/internal/secrets"
	"ssh-tunnel-manager/internal/supervisor"
)

const ringSize = 2000

type Options struct {
	DataDir   string
	HelperExe string
	Secrets   secrets.Store
	Askpass   *askpass.Server
	Start     supervisor.StartFunc
	Delay     func(attempt int) time.Duration
	// HostKeyTimeout bounds how long a live OpenSSH host key prompt waits for
	// the user; it must stay below sshd's LoginGraceTime (default 120 s).
	HostKeyTimeout time.Duration
	OnChange       func()
	OnLine         func(id string, l logs.Line)
	Autostart      func(enabled bool) error
	// Toast shows a notification; ToastDelay is the aggregation window (default 2 s).
	Toast      func(Toast)
	ToastDelay time.Duration
}

type TunnelView struct {
	Tunnel    config.Tunnel     `json:"tunnel"`
	Status    supervisor.Status `json:"status"`
	HasSecret bool              `json:"hasSecret"`
}

type HostKeyRequest struct {
	ID             string         `json:"id"`
	TunnelID       string         `json:"tunnelId"`
	TunnelName     string         `json:"tunnelName"`
	Backend        config.Backend `json:"backend"`
	Host           string         `json:"host"`
	KeyType        string         `json:"keyType,omitempty"`
	Fingerprint    string         `json:"fingerprint"`
	OldFingerprint string         `json:"oldFingerprint,omitempty"`
	Changed        bool           `json:"changed"`
}

type pending struct {
	req HostKeyRequest
	// answer is set for live OpenSSH prompts, where ssh.exe waits for the reply.
	answer chan bool
}

type logged struct {
	state   supervisor.State
	attempt int
}

type Manager struct {
	o       Options
	sup     *supervisor.Supervisor
	cfgPath string
	logFile *logs.RotatingFile
	toasts  *toaster

	mu        sync.Mutex
	cfg       config.Config
	engine    engine.Engine
	hasSecret map[string]bool
	rings     map[string]*logs.Ring
	requests  []*pending
	trusted   map[string]string
	lastLog   map[string]logged
}

// New loads the configuration and prepares the supervisor. When the config file
// is corrupt, the Manager is still returned (with an empty configuration) along
// with an error wrapping config.ErrCorrupt for the UI to show.
func New(o Options) (*Manager, error) {
	if o.HostKeyTimeout == 0 {
		o.HostKeyTimeout = 100 * time.Second
	}
	if o.ToastDelay == 0 {
		o.ToastDelay = 2 * time.Second
	}
	if err := os.MkdirAll(filepath.Join(o.DataDir, "logs"), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	tmp := filepath.Join(o.DataDir, "tmp")
	if err := pwfile.CleanDir(tmp); err != nil {
		return nil, fmt.Errorf("clean %s: %w", tmp, err)
	}
	lf, err := logs.OpenRotating(filepath.Join(o.DataDir, "logs", "ssh-tunnel-manager.log"), 5<<20, 3)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		o: o, cfgPath: filepath.Join(o.DataDir, "config.json"), logFile: lf,
		hasSecret: map[string]bool{}, rings: map[string]*logs.Ring{},
		trusted: map[string]string{}, lastLog: map[string]logged{},
	}
	cfg, loadErr := config.Load(m.cfgPath)
	if loadErr != nil && !errors.Is(loadErr, config.ErrCorrupt) {
		return nil, errors.Join(loadErr, lf.Close())
	}
	m.cfg = cfg
	m.engine = engine.Engine{
		DataDir: o.DataDir, HelperExe: o.HelperExe, Secrets: o.Secrets, Askpass: o.Askpass,
		HostKeyPrompt: m.hostKeyPrompt, Log: m.logText,
		ServerAliveInterval: 20, ServerAliveCountMax: 3, PlinkSession: engine.DefaultPlinkSession,
	}
	m.applyPathsLocked()
	m.toasts = newToaster(o.ToastDelay, func() bool { return o.Toast != nil && m.Settings().Notifications }, func(t Toast) {
		if o.Toast != nil {
			o.Toast(t)
		}
	})
	for _, t := range cfg.Tunnels {
		m.hasSecret[t.ID] = m.secretExists(t)
	}
	m.sup = supervisor.New(supervisor.Options{
		Prepare: m.prepare, Start: o.Start, Delay: o.Delay,
		OnStatus: m.onStatus, OnLine: m.addLine,
	})
	return m, loadErr
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (m *Manager) applyPathsLocked() {
	m.engine.SSHExe = orDefault(m.cfg.Settings.SSHPath, engine.DefaultSSHExe)
	m.engine.PlinkExe = orDefault(m.cfg.Settings.PlinkPath, engine.DefaultPlinkExe)
}

func (m *Manager) secretExists(t config.Tunnel) bool {
	_, err := m.o.Secrets.Get(engine.CredentialRef(t))
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		log.Printf("credential lookup for %s: %v", t.Name, err)
	}
	return err == nil
}

func (m *Manager) changed() {
	if m.o.OnChange != nil {
		m.o.OnChange()
	}
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return hex.EncodeToString(b)
}

func (m *Manager) prepare(t config.Tunnel) (backend.Options, func(), error) {
	m.mu.Lock()
	e := m.engine
	m.mu.Unlock()
	return e.Prepare(t)
}

// Close stops all tunnels and closes the log file.
func (m *Manager) Close() error {
	m.sup.StopAll()
	return m.logFile.Close()
}

func (m *Manager) tunnelLocked(id string) (config.Tunnel, int) {
	for i, t := range m.cfg.Tunnels {
		if t.ID == id {
			return t, i
		}
	}
	return config.Tunnel{}, -1
}

func (m *Manager) tunnel(id string) (config.Tunnel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, i := m.tunnelLocked(id)
	if i < 0 {
		return t, fmt.Errorf("tunnel %s not found", id)
	}
	return t, nil
}

func (m *Manager) status(id string) supervisor.Status {
	if st, ok := m.sup.Status(id); ok {
		return st
	}
	return supervisor.Status{ID: id, State: supervisor.Stopped}
}

func active(s supervisor.State) bool {
	return s == supervisor.Connecting || s == supervisor.Up || s == supervisor.Backoff
}

func (m *Manager) Tunnels() []TunnelView {
	m.mu.Lock()
	ts := slices.Clone(m.cfg.Tunnels)
	has := make([]bool, len(ts))
	for i, t := range ts {
		has[i] = m.hasSecret[t.ID]
	}
	m.mu.Unlock()
	out := make([]TunnelView, len(ts))
	for i, t := range ts {
		out[i] = TunnelView{Tunnel: t, Status: m.status(t.ID), HasSecret: has[i]}
	}
	return out
}

func (m *Manager) Settings() config.Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.Settings
}

func (m *Manager) SaveSettings(s config.Settings) error {
	s.SSHPath, s.PlinkPath = strings.TrimSpace(s.SSHPath), strings.TrimSpace(s.PlinkPath)
	for _, p := range []string{s.SSHPath, s.PlinkPath} {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			return fmt.Errorf("executable not found: %s", p)
		}
	}
	m.mu.Lock()
	wasOn := m.cfg.Settings.Autostart
	m.mu.Unlock()
	if m.o.Autostart != nil && (s.Autostart || wasOn) {
		if err := m.o.Autostart(s.Autostart); err != nil {
			return err
		}
	}
	m.mu.Lock()
	next := m.cfg
	next.Settings = s
	if err := config.Save(m.cfgPath, next); err != nil {
		m.mu.Unlock()
		return err
	}
	m.cfg = next
	m.applyPathsLocked()
	m.mu.Unlock()
	m.changed()
	return nil
}

func normalize(t config.Tunnel) config.Tunnel {
	t.Name, t.Host, t.User = strings.TrimSpace(t.Name), strings.TrimSpace(t.Host), strings.TrimSpace(t.User)
	t.Auth.KeyPath = strings.TrimSpace(t.Auth.KeyPath)
	t.Forward.BindAddress, t.Forward.TargetHost = strings.TrimSpace(t.Forward.BindAddress), strings.TrimSpace(t.Forward.TargetHost)
	if t.Backend == "" {
		t.Backend = config.BackendOpenSSH
	}
	if t.Auth.Method == config.AuthAgent && t.Auth.Agent == "" {
		t.Auth.Agent = config.AgentPageant
	}
	if t.Auth.Method != config.AuthAgent {
		t.Auth.Agent = ""
	}
	if t.Auth.Method != config.AuthKey {
		t.Auth.KeyPath = ""
	}
	if t.Server.Mode == "" {
		t.Server.Mode = config.ServerNone
	}
	return t
}

// SaveTunnel creates (empty ID) or updates a tunnel. secret: nil keeps the
// stored password/passphrase, "" deletes it, anything else replaces it.
// A running tunnel is reconnected with the new settings.
func (m *Manager) SaveTunnel(t config.Tunnel, secret *string) (config.Tunnel, error) {
	t = normalize(t)
	isNew := t.ID == ""
	if isNew {
		t.ID = newID()
	}
	if err := t.Validate(); err != nil {
		return t, err
	}
	m.mu.Lock()
	next := m.cfg
	next.Tunnels = slices.Clone(m.cfg.Tunnels)
	old, i := m.tunnelLocked(t.ID)
	switch {
	case isNew:
		next.Tunnels = append(next.Tunnels, t)
	case i < 0:
		m.mu.Unlock()
		return t, fmt.Errorf("tunnel %s not found", t.ID)
	default:
		if old.Host != t.Host || old.Port != t.Port || old.Backend != t.Backend {
			t.HostKey = config.HostKey{}
		} else {
			t.HostKey = old.HostKey
		}
		next.Tunnels[i] = t
	}
	if err := config.Save(m.cfgPath, next); err != nil {
		m.mu.Unlock()
		return t, err
	}
	m.cfg = next
	m.mu.Unlock()

	var secretErr error
	if secret != nil {
		ref := engine.CredentialRef(t)
		if *secret == "" {
			if err := m.o.Secrets.Delete(ref); err != nil && !errors.Is(err, secrets.ErrNotFound) {
				secretErr = err
			}
		} else {
			secretErr = m.o.Secrets.Set(ref, *secret)
		}
	}
	has := m.secretExists(t)
	m.mu.Lock()
	m.hasSecret[t.ID] = has
	m.mu.Unlock()

	if !isNew && active(m.status(t.ID).State) {
		m.sup.Stop(t.ID)
		m.sup.Start(t)
	}
	m.changed()
	if secretErr != nil {
		return t, fmt.Errorf("tunnel saved, but storing the secret failed: %w", secretErr)
	}
	return t, nil
}

// DeleteTunnel stops the tunnel, removes it from the config and deletes its stored secret.
func (m *Manager) DeleteTunnel(id string) error {
	t, err := m.tunnel(id)
	if err != nil {
		return err
	}
	m.sup.Remove(id)
	m.mu.Lock()
	m.cancelRequestsLocked(id)
	next := m.cfg
	next.Tunnels = slices.DeleteFunc(slices.Clone(m.cfg.Tunnels), func(x config.Tunnel) bool { return x.ID == id })
	if err := config.Save(m.cfgPath, next); err != nil {
		m.mu.Unlock()
		return err
	}
	m.cfg = next
	delete(m.hasSecret, id)
	delete(m.rings, id)
	delete(m.lastLog, id)
	delete(m.trusted, id)
	m.mu.Unlock()
	m.toasts.forget(id)
	m.changed()
	if err := m.o.Secrets.Delete(engine.CredentialRef(t)); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return fmt.Errorf("tunnel deleted, but removing its secret failed: %w", err)
	}
	return nil
}

func (m *Manager) Connect(id string) error {
	t, err := m.tunnel(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.cancelRequestsLocked(id)
	m.mu.Unlock()
	m.sup.Start(t)
	return nil
}

func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	m.cancelRequestsLocked(id)
	m.mu.Unlock()
	m.sup.Stop(id)
	m.changed()
}

// ConnectAutoConnect starts every tunnel marked autoConnect.
func (m *Manager) ConnectAutoConnect() {
	m.mu.Lock()
	ts := slices.Clone(m.cfg.Tunnels)
	m.mu.Unlock()
	for _, t := range ts {
		if t.AutoConnect {
			m.sup.Start(t)
		}
	}
}

// RestartActive reconnects every connecting, up or backing-off tunnel now with
// backoff reset (after resume from sleep or a network change).
func (m *Manager) RestartActive(reason string) {
	for _, v := range m.Tunnels() {
		if active(v.Status.State) {
			m.logText(v.Tunnel.ID, reason+" → restart")
			m.sup.Restart(v.Tunnel.ID)
		}
	}
}

func (m *Manager) DisconnectAll() {
	m.mu.Lock()
	for _, t := range m.cfg.Tunnels {
		m.cancelRequestsLocked(t.ID)
	}
	m.mu.Unlock()
	m.sup.StopAll()
	m.changed()
}

// Logs returns the tunnel's recent lines, oldest first.
func (m *Manager) Logs(id string, verbose bool) []logs.Line {
	m.mu.Lock()
	r := m.rings[id]
	m.mu.Unlock()
	if r == nil {
		return []logs.Line{}
	}
	return r.Snapshot(verbose)
}

func (m *Manager) addLine(id string, l logs.Line) {
	m.mu.Lock()
	r := m.rings[id]
	if r == nil {
		r = logs.NewRing(ringSize)
		m.rings[id] = r
	}
	t, _ := m.tunnelLocked(id)
	m.mu.Unlock()
	r.Add(l)
	if err := m.logFile.WriteLine(orDefault(t.Name, id), l); err != nil {
		log.Printf("write tunnel log: %v", err)
	}
	if m.o.OnLine != nil {
		m.o.OnLine(id, l)
	}
}

func (m *Manager) logText(id, text string) {
	m.addLine(id, logs.Line{Time: time.Now(), Text: text})
}

func (m *Manager) onStatus(st supervisor.Status) {
	m.logTransition(st)
	if t, err := m.tunnel(st.ID); err == nil {
		m.toasts.observe(t.Name, st)
	}
	if st.State != supervisor.Connecting {
		m.mu.Lock()
		m.cancelLiveLocked(st.ID)
		m.mu.Unlock()
	}
	if st.State == supervisor.Error {
		m.raiseHostKeyRequest(st)
	}
	m.changed()
}

func (m *Manager) logTransition(st supervisor.Status) {
	cur := logged{st.State, st.Attempt}
	m.mu.Lock()
	prev, seen := m.lastLog[st.ID]
	m.lastLog[st.ID] = cur
	m.mu.Unlock()
	if seen && prev == cur {
		return
	}
	var text string
	switch st.State {
	case supervisor.Connecting:
		text = "connecting"
		if st.Attempt > 0 {
			text = fmt.Sprintf("reconnect attempt %d", st.Attempt)
		}
	case supervisor.Up:
		text = fmt.Sprintf("connected (pid %d)", st.PID)
	case supervisor.Backoff:
		text = fmt.Sprintf("disconnected (%s): %s; retry in %s", st.ErrorKind, st.LastError,
			time.Until(st.NextRetry).Round(100*time.Millisecond))
	case supervisor.Error:
		text = fmt.Sprintf("error (%s): %s", st.ErrorKind, st.LastError)
	case supervisor.Stopped:
		text = "stopped"
	}
	m.logText(st.ID, text)
}

func (m *Manager) HostKeyRequests() []HostKeyRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]HostKeyRequest, len(m.requests))
	for i, p := range m.requests {
		out[i] = p.req
	}
	return out
}

func (m *Manager) takeRequestLocked(reqID string) *pending {
	i := slices.IndexFunc(m.requests, func(p *pending) bool { return p.req.ID == reqID })
	if i < 0 {
		return nil
	}
	p := m.requests[i]
	m.requests = slices.Delete(m.requests, i, i+1)
	return p
}

// cancelLiveLocked rejects live OpenSSH prompts of the tunnel (its ssh.exe is gone).
func (m *Manager) cancelLiveLocked(tunnelID string) {
	m.requests = slices.DeleteFunc(m.requests, func(p *pending) bool {
		if p.req.TunnelID != tunnelID || p.answer == nil {
			return false
		}
		p.answer <- false
		return true
	})
}

func (m *Manager) cancelRequestsLocked(tunnelID string) {
	m.requests = slices.DeleteFunc(m.requests, func(p *pending) bool {
		if p.req.TunnelID != tunnelID {
			return false
		}
		if p.answer != nil {
			p.answer <- false
		}
		return true
	})
}

// hostKeyPrompt is the engine callback for OpenSSH's "authenticity of host"
// prompt. It blocks the askpass reply until the user answers in the GUI.
func (m *Manager) hostKeyPrompt(r engine.HostKeyRequest) bool {
	m.mu.Lock()
	if fp, ok := m.trusted[r.TunnelID]; ok && fp == r.Fingerprint {
		delete(m.trusted, r.TunnelID)
		m.mu.Unlock()
		m.logText(r.TunnelID, "host key accepted (confirmed in the changed-key dialog): "+r.Fingerprint)
		return true
	}
	p := &pending{
		req: HostKeyRequest{ID: newID(), TunnelID: r.TunnelID, TunnelName: r.TunnelName, Backend: config.BackendOpenSSH,
			Host: r.Host, KeyType: r.KeyType, Fingerprint: r.Fingerprint},
		answer: make(chan bool, 1),
	}
	m.requests = append(m.requests, p)
	m.mu.Unlock()
	m.changed()

	timer := time.NewTimer(m.o.HostKeyTimeout)
	defer timer.Stop()
	select {
	case a := <-p.answer:
		return a
	case <-timer.C:
		m.mu.Lock()
		taken := m.takeRequestLocked(p.req.ID)
		m.mu.Unlock()
		if taken == nil {
			return <-p.answer
		}
		m.logText(r.TunnelID, "host key prompt timed out")
		m.changed()
		return false
	}
}

// raiseHostKeyRequest turns a host key error into a pending dialog: a changed
// key (any backend) or an unknown plink key. Unknown OpenSSH keys are asked
// live through askpass, so their error means the user already rejected them.
func (m *Manager) raiseHostKeyRequest(st supervisor.Status) bool {
	changed := st.ErrorKind == backend.KindHostKeyChanged.String()
	unknown := st.ErrorKind == backend.KindHostKeyUnknown.String()
	if st.State != supervisor.Error || st.Fingerprint == "" || !(changed || unknown) {
		return false
	}
	m.mu.Lock()
	t, i := m.tunnelLocked(st.ID)
	e := m.engine
	m.mu.Unlock()
	if i < 0 || (unknown && t.Backend != config.BackendPlink) {
		return false
	}
	req := HostKeyRequest{ID: newID(), TunnelID: t.ID, TunnelName: t.Name, Backend: t.Backend,
		Host: hostkey.KnownHostName(t.Host, t.Port), Fingerprint: st.Fingerprint, Changed: changed}
	if changed {
		if t.Backend == config.BackendPlink {
			req.OldFingerprint = t.HostKey.PlinkFingerprint
		} else if fp, err := e.KnownOpenSSHFingerprint(t); err != nil {
			m.logText(t.ID, "read stored host key: "+err.Error())
		} else {
			req.OldFingerprint = fp
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if slices.ContainsFunc(m.requests, func(p *pending) bool { return p.req.TunnelID == t.ID }) {
		return false
	}
	m.requests = append(m.requests, &pending{req: req})
	return true
}

// ReviewHostKey reopens the host key dialog for a tunnel stopped by a host key error.
func (m *Manager) ReviewHostKey(tunnelID string) error {
	if !m.raiseHostKeyRequest(m.status(tunnelID)) {
		return errors.New("no host key decision pending for this tunnel")
	}
	m.changed()
	return nil
}

// AnswerHostKey applies the user's decision. Accepting stores the key (plink
// fingerprint in config; OpenSSH via known_hosts) and reconnects.
func (m *Manager) AnswerHostKey(reqID string, accept bool) error {
	m.mu.Lock()
	p := m.takeRequestLocked(reqID)
	m.mu.Unlock()
	if p == nil {
		return errors.New("host key request is no longer pending")
	}
	if p.answer != nil {
		p.answer <- accept
		m.changed()
		return nil
	}
	r := p.req
	if !accept {
		m.logText(r.TunnelID, "host key rejected by user: "+r.Fingerprint)
		m.changed()
		return nil
	}
	t, err := m.tunnel(r.TunnelID)
	if err != nil {
		return err
	}
	if t.Backend == config.BackendPlink {
		t.HostKey.PlinkFingerprint = r.Fingerprint
		if err := m.replaceTunnel(t); err != nil {
			return err
		}
	} else if r.Changed {
		m.mu.Lock()
		e := m.engine
		m.mu.Unlock()
		if err := e.ForgetOpenSSHHostKey(t); err != nil {
			return err
		}
		m.mu.Lock()
		m.trusted[t.ID] = r.Fingerprint
		m.mu.Unlock()
	}
	m.logText(t.ID, "host key accepted by user: "+r.Fingerprint)
	m.sup.Start(t)
	m.changed()
	return nil
}

func (m *Manager) replaceTunnel(t config.Tunnel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, i := m.tunnelLocked(t.ID)
	if i < 0 {
		return fmt.Errorf("tunnel %s not found", t.ID)
	}
	next := m.cfg
	next.Tunnels = slices.Clone(m.cfg.Tunnels)
	next.Tunnels[i] = t
	if err := config.Save(m.cfgPath, next); err != nil {
		return err
	}
	m.cfg = next
	return nil
}
