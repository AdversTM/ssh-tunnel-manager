package main

import (
	"log"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"ssh-tunnel-manager/internal/logs"
	"ssh-tunnel-manager/internal/manager"
	"ssh-tunnel-manager/internal/winsys"
)

// ui owns the window, the tray icon/menu and the push of state to the frontend.
type ui struct {
	app   *application.App
	win   *application.WebviewWindow
	tray  *application.SystemTray
	mgr   *manager.Manager
	icons map[manager.Icon][]byte

	started  atomic.Bool
	quitting atomic.Bool
	closer   sync.Once
	dirty    chan struct{}

	lastMenu  []manager.MenuTunnel
	lastIcon  manager.Icon
	lastTip   string
	shownReqs map[string]bool

	mu         sync.Mutex
	startupErr string
	watchers   []func() error
}

func (u *ui) startWatchers() {
	stopResume, err := winsys.WatchResume(func() { u.mgr.RestartActive("resume") })
	if err != nil {
		log.Printf("watch resume: %v", err)
	}
	stopNet, err := winsys.WatchNetwork(3*time.Second, func(online bool) {
		if online {
			u.mgr.RestartActive("network change")
		}
	}, func(err error) { log.Printf("network fingerprint: %v", err) })
	if err != nil {
		log.Printf("watch network: %v", err)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, stop := range []func() error{stopResume, stopNet} {
		if stop != nil {
			u.watchers = append(u.watchers, stop)
		}
	}
}

func (u *ui) stopWatchers() {
	u.mu.Lock()
	ws := u.watchers
	u.watchers = nil
	u.mu.Unlock()
	for _, stop := range ws {
		if err := stop(); err != nil {
			log.Printf("stop watcher: %v", err)
		}
	}
}

func (u *ui) startupError() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.startupErr
}

func (u *ui) setStartupError(s string) {
	u.mu.Lock()
	u.startupErr = s
	u.mu.Unlock()
	u.notify()
}

func (u *ui) state() State {
	return State{Tunnels: u.mgr.Tunnels(), HostKeyRequests: u.mgr.HostKeyRequests(), StartupError: u.startupError()}
}

func (u *ui) setup(show bool) error {
	icons, err := generateIcons()
	if err != nil {
		return err
	}
	u.icons = icons

	u.win = u.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: "SSH Tunnel Manager",
		Width: 1040, Height: 720, MinWidth: 760, MinHeight: 520,
		Hidden: !show, URL: "/",
	})
	// The close button hides to tray; only Exit (quitting) really closes.
	u.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if u.quitting.Load() {
			return
		}
		u.win.Hide()
		e.Cancel()
	})

	u.tray = u.app.SystemTray.New()
	u.tray.OnClick(u.toggleWindow)
	u.tray.OnRightClick(func() { u.tray.OpenMenu() })
	u.refreshTray(u.mgr.Tunnels())

	u.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		u.started.Store(true)
		go u.pushLoop()
		u.notify()
		u.mgr.ConnectAutoConnect()
		u.startWatchers()
	})
	return nil
}

// notify is the manager's OnChange hook; it must never block the caller.
func (u *ui) notify() {
	select {
	case u.dirty <- struct{}{}:
	default:
	}
}

// pushLoop coalesces bursts of changes into one frontend event + tray refresh.
func (u *ui) pushLoop() {
	for range u.dirty {
		if u.quitting.Load() {
			return
		}
		st := u.state()
		u.app.Event.Emit(stateEvent, st)
		u.refreshTray(st.Tunnels)
		hasUnshownRequest := false
		pending := make(map[string]bool, len(st.HostKeyRequests))
		for _, r := range st.HostKeyRequests {
			pending[r.ID] = true
			hasUnshownRequest = hasUnshownRequest || !u.shownReqs[r.ID]
		}
		u.shownReqs = pending
		if hasUnshownRequest {
			u.showWindow()
		}
	}
}

func (u *ui) line(id string, l logs.Line) {
	if u.started.Load() && !u.quitting.Load() {
		u.app.Event.Emit(logEvent, LogEvent{TunnelID: id, Line: l})
	}
}

func (u *ui) showWindow() {
	u.win.Show()
	u.win.UnMinimise()
	u.win.Focus()
}

func (u *ui) toggleWindow() {
	if u.win.IsVisible() && !u.win.IsMinimised() {
		u.win.Hide()
		return
	}
	u.showWindow()
}

func (u *ui) refreshTray(views []manager.TunnelView) {
	if icon := manager.OverallIcon(views); icon != u.lastIcon {
		u.tray.SetIcon(u.icons[icon])
		u.lastIcon = icon
	}
	if tip := manager.Tooltip(views); tip != u.lastTip {
		u.tray.SetTooltip(tip)
		u.lastTip = tip
	}
	model := manager.MenuModel(views)
	if u.lastMenu != nil && slices.Equal(model, u.lastMenu) {
		return
	}
	u.lastMenu = model
	u.tray.SetMenu(u.buildMenu(model))
}

func (u *ui) buildMenu(model []manager.MenuTunnel) *application.Menu {
	m := u.app.Menu.New()
	for _, t := range model {
		id := t.ID
		sub := m.AddSubmenu(t.Name)
		sub.Add(t.Status).SetEnabled(false)
		sub.AddCheckbox(t.ToggleLabel, t.Active).OnClick(func(*application.Context) {
			go u.toggle(id, t.Active)
		})
		sub.Add("Show logs").OnClick(func(*application.Context) {
			u.showWindow()
			u.app.Event.Emit(showLogsEvent, id)
		})
	}
	if len(model) > 0 {
		m.AddSeparator()
	}
	m.Add("Open manager").OnClick(func(*application.Context) { u.showWindow() })
	m.Add("Connect all auto-connect").OnClick(func(*application.Context) { go u.mgr.ConnectAutoConnect() })
	m.Add("Disconnect all").OnClick(func(*application.Context) { go u.mgr.DisconnectAll() })
	m.AddSeparator()
	m.Add("Exit").OnClick(func(*application.Context) { u.quit() })
	return m
}

func (u *ui) toggle(id string, active bool) {
	if active {
		u.mgr.Disconnect(id)
		return
	}
	if err := u.mgr.Connect(id); err != nil {
		log.Printf("connect %s: %v", id, err)
	}
}

func (u *ui) quit() {
	u.quitting.Store(true)
	u.app.Quit()
}

// shutdown stops every tunnel before the process exits (Exit menu or OS logoff).
func (u *ui) shutdown() {
	u.quitting.Store(true)
	u.closer.Do(func() {
		u.stopWatchers()
		if err := u.mgr.Close(); err != nil {
			log.Printf("close manager: %v", err)
		}
	})
}
