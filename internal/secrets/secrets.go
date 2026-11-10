package secrets

import (
	"errors"
	"fmt"

	"github.com/danieljoos/wincred"
)

var ErrNotFound = errors.New("secret not found")

type Store interface {
	Get(ref string) (string, error)
	Set(ref, secret string) error
	Delete(ref string) error
}

// WinCred stores secrets as generic credentials in Windows Credential Manager
// (DPAPI-protected, current user, not roaming).
type WinCred struct{}

func (WinCred) Get(ref string) (string, error) {
	c, err := wincred.GetGenericCredential(ref)
	if errors.Is(err, wincred.ErrElementNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read credential %s: %w", ref, err)
	}
	return string(c.CredentialBlob), nil
}

func (WinCred) Set(ref, secret string) error {
	c := wincred.NewGenericCredential(ref)
	c.CredentialBlob = []byte(secret)
	c.Persist = wincred.PersistLocalMachine
	c.Comment = "SSH Tunnel Manager"
	if err := c.Write(); err != nil {
		return fmt.Errorf("write credential %s: %w", ref, err)
	}
	return nil
}

func (WinCred) Delete(ref string) error {
	c, err := wincred.GetGenericCredential(ref)
	if errors.Is(err, wincred.ErrElementNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read credential %s: %w", ref, err)
	}
	if err := c.Delete(); err != nil {
		return fmt.Errorf("delete credential %s: %w", ref, err)
	}
	return nil
}
