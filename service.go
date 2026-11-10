package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/logs"
	"ssh-tunnel-manager/internal/manager"
	"ssh-tunnel-manager/internal/serverkit"
)

const (
	stateEvent    = "state"
	logEvent      = "log"
	showLogsEvent = "ui:show-logs"
)

type State struct {
	Tunnels         []manager.TunnelView     `json:"tunnels"`
	HostKeyRequests []manager.HostKeyRequest `json:"hostKeyRequests"`
	StartupError    string                   `json:"startupError"`
}

type LogEvent struct {
	TunnelID string    `json:"tunnelId"`
	Line     logs.Line `json:"line"`
}

func init() {
	application.RegisterEvent[State](stateEvent)
	application.RegisterEvent[LogEvent](logEvent)
	application.RegisterEvent[string](showLogsEvent)
}

// TunnelService is the API bound to the Svelte frontend.
type TunnelService struct {
	mgr *manager.Manager
	ui  *ui
}

func (s *TunnelService) Snapshot() State { return s.ui.state() }

func (s *TunnelService) DismissStartupError() { s.ui.setStartupError("") }

// NewTunnel returns form defaults for a new tunnel (OpenSSH, key auth, -R on localhost).
func (s *TunnelService) NewTunnel() config.Tunnel {
	return config.Tunnel{
		Backend: config.BackendOpenSSH, Port: 22,
		Auth:    config.Auth{Method: config.AuthKey},
		Forward: config.Forward{Type: config.ForwardRemote, BindAddress: "127.0.0.1", TargetHost: "127.0.0.1"},
		Server:  config.Server{Mode: config.ServerNone},
	}
}

// SaveTunnel creates or updates a tunnel; secret nil keeps, "" deletes, else replaces.
func (s *TunnelService) SaveTunnel(t config.Tunnel, secret *string) (config.Tunnel, error) {
	return s.mgr.SaveTunnel(t, secret)
}

func (s *TunnelService) DeleteTunnel(id string) error { return s.mgr.DeleteTunnel(id) }
func (s *TunnelService) Connect(id string) error      { return s.mgr.Connect(id) }
func (s *TunnelService) Disconnect(id string)         { s.mgr.Disconnect(id) }

func (s *TunnelService) Logs(id string, verbose bool) []logs.Line { return s.mgr.Logs(id, verbose) }

func (s *TunnelService) Settings() config.Settings { return s.mgr.Settings() }
func (s *TunnelService) SaveSettings(st config.Settings) error {
	return s.mgr.SaveSettings(st)
}

func (s *TunnelService) AnswerHostKey(requestID string, accept bool) error {
	return s.mgr.AnswerHostKey(requestID, accept)
}
func (s *TunnelService) ReviewHostKey(tunnelID string) error { return s.mgr.ReviewHostKey(tunnelID) }

// ServerKit generates the server setup for a saved tunnel. pubKey overrides the
// key read from the tunnel's key file ("" = use the key file, if any).
func (s *TunnelService) ServerKit(id, pubKey string) (serverkit.Kit, error) {
	for _, v := range s.mgr.Tunnels() {
		if v.Tunnel.ID != id {
			continue
		}
		t := v.Tunnel
		if pubKey == "" && t.Auth.Method == config.AuthKey {
			if k, err := serverkit.PublicKey(t.Auth.KeyPath); err == nil {
				pubKey = k
			}
		}
		return serverkit.Generate(t, pubKey)
	}
	return serverkit.Kit{}, fmt.Errorf("tunnel %s not found", id)
}

// SaveText asks for a file name and writes content with LF line endings; returns "" when cancelled.
func (s *TunnelService) SaveText(name, content string) (string, error) {
	path, err := s.ui.app.Dialog.SaveFile().
		SetFilename(name).
		AttachToWindow(s.ui.win).
		PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(strings.ReplaceAll(content, "\r\n", "\n")), 0o644)
}

// PickFile opens a native file dialog; returns "" when cancelled.
func (s *TunnelService) PickFile(title string) (string, error) {
	return s.ui.app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseFiles(true).
		AttachToWindow(s.ui.win).
		PromptForSingleSelection()
}
