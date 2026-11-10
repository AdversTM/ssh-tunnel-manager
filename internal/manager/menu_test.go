package manager

import (
	"testing"

	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/supervisor"
)

func view(name string, st supervisor.State, attempt int, kind string) TunnelView {
	return TunnelView{Tunnel: config.Tunnel{ID: name, Name: name},
		Status: supervisor.Status{ID: name, State: st, Attempt: attempt, ErrorKind: kind}}
}

func TestOverallIcon(t *testing.T) {
	cases := []struct {
		states []supervisor.State
		want   Icon
	}{
		{nil, IconIdle},
		{[]supervisor.State{supervisor.Stopped, supervisor.Stopped}, IconIdle},
		{[]supervisor.State{supervisor.Up, supervisor.Stopped}, IconUp},
		{[]supervisor.State{supervisor.Up, supervisor.Backoff}, IconBusy},
		{[]supervisor.State{supervisor.Connecting, supervisor.Up}, IconBusy},
		{[]supervisor.State{supervisor.Up, supervisor.Error, supervisor.Backoff}, IconError},
	}
	for _, c := range cases {
		var vs []TunnelView
		for i, s := range c.states {
			vs = append(vs, view(string(rune('a'+i)), s, 0, ""))
		}
		if got := OverallIcon(vs); got != c.want {
			t.Errorf("%v: got %s want %s", c.states, got, c.want)
		}
	}
}

func TestMenuModel(t *testing.T) {
	got := MenuModel([]TunnelView{
		view("web", supervisor.Up, 0, ""),
		view("db", supervisor.Stopped, 0, ""),
		view("api", supervisor.Backoff, 3, "remote-port-busy"),
		view("vpn", supervisor.Error, 0, "host-key-changed"),
		view("git", supervisor.Connecting, 2, "network"),
	})
	want := []MenuTunnel{
		{ID: "web", Name: "web", Status: "Status: Up", Active: true, ToggleLabel: "Disconnect"},
		{ID: "db", Name: "db", Status: "Status: Stopped", Active: false, ToggleLabel: "Connect"},
		{ID: "api", Name: "api", Status: "Status: Server port busy, retrying (attempt 3)", Active: true, ToggleLabel: "Disconnect"},
		{ID: "vpn", Name: "vpn", Status: "Status: Error: HOST KEY CHANGED", Active: false, ToggleLabel: "Connect"},
		{ID: "git", Name: "git", Status: "Status: Reconnecting (attempt 2)", Active: true, ToggleLabel: "Disconnect"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestTooltip(t *testing.T) {
	if got := Tooltip(nil); got != "SSH Tunnel Manager - no tunnels" {
		t.Fatal(got)
	}
	got := Tooltip([]TunnelView{view("a", supervisor.Up, 0, ""), view("b", supervisor.Error, 0, "auth"), view("c", supervisor.Stopped, 0, "")})
	if got != "SSH Tunnel Manager - 1/3 up, 1 error" {
		t.Fatal(got)
	}
}
