// Package winsys integrates with Windows: autostart (HKCU Run), resume from
// sleep and network change notifications.
package winsys

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
	// AutostartName is the value name under HKCU\...\Run.
	AutostartName = "SshTunnelManager"
)

// Command quotes exe and args for a Run entry.
func Command(exe string, args ...string) string {
	parts := []string{`"` + exe + `"`}
	for _, a := range args {
		if strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

func SetAutostart(name, command string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(name, command); err != nil {
		return fmt.Errorf("set autostart: %w", err)
	}
	return nil
}

func RemoveAutostart(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("remove autostart: %w", err)
	}
	return nil
}

// Autostart returns the registered command, or "" when there is none.
func Autostart(name string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	return v, err
}
