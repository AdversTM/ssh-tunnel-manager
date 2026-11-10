package manager

import (
	"fmt"

	"ssh-tunnel-manager/internal/supervisor"
)

type Icon string

const (
	IconIdle  Icon = "idle"  // grey: nothing connected
	IconUp    Icon = "up"    // green: every enabled tunnel is up
	IconBusy  Icon = "busy"  // yellow: something is connecting or reconnecting
	IconError Icon = "error" // red: something needs user action
)

// OverallIcon picks the tray colour; error wins over busy, busy over up.
func OverallIcon(views []TunnelView) Icon {
	icon := IconIdle
	for _, v := range views {
		switch v.Status.State {
		case supervisor.Error:
			return IconError
		case supervisor.Connecting, supervisor.Backoff:
			icon = IconBusy
		case supervisor.Up:
			if icon == IconIdle {
				icon = IconUp
			}
		}
	}
	return icon
}

var errorLabels = map[string]string{
	"network":          "network error",
	"remote-port-busy": "server port busy",
	"local-port-busy":  "local port busy",
	"config":           "configuration error",
	"auth":             "authentication failed",
	"host-key-unknown": "host key not trusted",
	"host-key-changed": "HOST KEY CHANGED",
}

func errorLabel(kind string) string {
	if l, ok := errorLabels[kind]; ok {
		return l
	}
	return kind
}

// StatusText is the short human status used in the tray menu and tooltip.
// It never contains countdowns so the menu only changes on real transitions.
func StatusText(st supervisor.Status) string {
	switch st.State {
	case supervisor.Connecting:
		if st.Attempt > 0 {
			return fmt.Sprintf("Reconnecting (attempt %d)", st.Attempt)
		}
		return "Connecting"
	case supervisor.Up:
		return "Up"
	case supervisor.Backoff:
		if st.ErrorKind == "remote-port-busy" {
			return fmt.Sprintf("Server port busy, retrying (attempt %d)", st.Attempt)
		}
		return fmt.Sprintf("Reconnecting (attempt %d)", st.Attempt)
	case supervisor.Error:
		return "Error: " + errorLabel(st.ErrorKind)
	}
	return "Stopped"
}

type MenuTunnel struct {
	ID          string
	Name        string
	Status      string
	Active      bool
	ToggleLabel string
}

func MenuModel(views []TunnelView) []MenuTunnel {
	out := make([]MenuTunnel, len(views))
	for i, v := range views {
		on := active(v.Status.State)
		label := "Connect"
		if on {
			label = "Disconnect"
		}
		out[i] = MenuTunnel{ID: v.Tunnel.ID, Name: v.Tunnel.Name, Status: "Status: " + StatusText(v.Status), Active: on, ToggleLabel: label}
	}
	return out
}

func Tooltip(views []TunnelView) string {
	if len(views) == 0 {
		return "SSH Tunnel Manager - no tunnels"
	}
	up, errs := 0, 0
	for _, v := range views {
		switch v.Status.State {
		case supervisor.Up:
			up++
		case supervisor.Error:
			errs++
		}
	}
	s := fmt.Sprintf("SSH Tunnel Manager - %d/%d up", up, len(views))
	if errs > 0 {
		s += fmt.Sprintf(", %d error", errs)
	}
	return s
}
