package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"ssh-tunnel-manager/internal/askpass"
	"ssh-tunnel-manager/internal/config"
	"ssh-tunnel-manager/internal/engine"
	"ssh-tunnel-manager/internal/logs"
	"ssh-tunnel-manager/internal/manager"
	"ssh-tunnel-manager/internal/secrets"
	"ssh-tunnel-manager/internal/winsys"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// ssh.exe runs this exe as SSH_ASKPASS; answer before any app/single-instance setup.
	if askpass.IsHelperInvocation() {
		os.Exit(askpass.RunHelper(os.Args[1:], os.Stdout, os.Stderr))
	}
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func defaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "SshTunnelManager"), nil
}

func run() error {
	def, err := defaultDataDir()
	if err != nil {
		return err
	}
	dataDir := flag.String("data", def, "data directory (config, known_hosts, logs)")
	show := flag.Bool("show", false, "show the manager window at start")
	flag.Parse()

	if err := os.MkdirAll(filepath.Join(*dataDir, "logs"), 0o700); err != nil {
		return err
	}
	appLog, err := logs.OpenRotating(filepath.Join(*dataDir, "logs", "app.log"), 1<<20, 2)
	if err != nil {
		return err
	}
	defer appLog.Close()
	log.SetOutput(appLog)

	if err := engine.EnsurePlinkSession(engine.DefaultPlinkSession, 20); err != nil {
		log.Printf("plink keepalive session: %v", err)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	ap, err := askpass.NewServer()
	if err != nil {
		return err
	}
	defer ap.Close()

	var autostartArgs []string
	if *dataDir != def {
		autostartArgs = []string{"-data", *dataDir}
	}
	autostartCmd := winsys.Command(self, autostartArgs...)
	applyAutostart := func(on bool) error {
		if on {
			return winsys.SetAutostart(winsys.AutostartName, autostartCmd)
		}
		return winsys.RemoveAutostart(winsys.AutostartName)
	}

	notifier := notifications.New()
	ui := &ui{dirty: make(chan struct{}, 1)}
	mgr, err := manager.New(manager.Options{
		DataDir: *dataDir, HelperExe: self, Secrets: secrets.WinCred{}, Askpass: ap,
		OnChange:  ui.notify,
		OnLine:    ui.line,
		Autostart: applyAutostart,
		Toast:     func(t manager.Toast) { sendToast(notifier, t) },
	})
	var startupErr string
	if errors.Is(err, config.ErrCorrupt) {
		startupErr = fmt.Sprintf("The configuration file could not be read and was set aside; starting with an empty configuration.\n%v", err)
		log.Print(startupErr)
	} else if err != nil {
		return err
	}
	ui.mgr = mgr
	ui.startupErr = startupErr
	svc := &TunnelService{mgr: mgr, ui: ui}
	if mgr.Settings().Autostart {
		if err := applyAutostart(true); err != nil {
			log.Printf("refresh autostart entry: %v", err)
		}
	}
	notifier.OnNotificationResponse(func(notifications.NotificationResult) { ui.showWindow() })

	ui.app = application.New(application.Options{
		Name:        "ssh-tunnel-manager",
		Description: "SSH Tunnel Manager",
		Services:    []application.Service{application.NewService(svc), application.NewService(notifier)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "com.sshtunnelmanager.app",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { ui.showWindow() },
		},
		OnShutdown: ui.shutdown,
		Windows: application.WindowsOptions{
			AdditionalBrowserArgs: browserArgs(),
			WebviewUserDataPath:   filepath.Join(*dataDir, "webview"),
		},
	})
	if err := ui.setup(*show || startupErr != ""); err != nil {
		return err
	}
	return ui.app.Run()
}

func sendToast(n *notifications.NotificationService, t manager.Toast) {
	log.Printf("toast: %s | %s", t.Title, strings.ReplaceAll(t.Body, "\n", " / "))
	err := n.SendNotification(notifications.NotificationOptions{
		ID: fmt.Sprintf("stm-%d", time.Now().UnixNano()), Title: t.Title, Body: t.Body,
	})
	if err != nil {
		log.Printf("toast failed: %v", err)
	}
}
