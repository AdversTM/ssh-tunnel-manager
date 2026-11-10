// Package pwfile writes plink -pwfile files readable only by the current user.
package pwfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func currentUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("token user: %w", err)
	}
	return u.User.Sid.String(), nil
}

// OwnerOnlySDDL grants full access to the current user only, no inheritance.
func OwnerOnlySDDL() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return "D:P(A;;FA;;;" + sid + ")", nil
}

// Create writes secret to a new random file in dir. remove is idempotent.
func Create(dir, secret string) (path string, remove func(), err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create %s: %w", dir, err)
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	path = filepath.Join(dir, "pw-"+hex.EncodeToString(b))

	sddl, err := OwnerOnlySDDL()
	if err != nil {
		return "", nil, err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return "", nil, fmt.Errorf("security descriptor: %w", err)
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, &sa, windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_ATTRIBUTE_TEMPORARY, 0)
	if err != nil {
		return "", nil, fmt.Errorf("create password file: %w", err)
	}
	f := os.NewFile(uintptr(h), path)
	_, werr := f.WriteString(secret + "\n")
	if err := errors.Join(werr, f.Close()); err != nil {
		return "", nil, errors.Join(fmt.Errorf("write password file: %w", err), os.Remove(path))
	}
	var once sync.Once
	remove = func() {
		once.Do(func() {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "remove password file %s: %v\n", path, err)
			}
		})
	}
	return path, remove, nil
}

// CleanDir removes leftover password files (e.g. after a crash).
func CleanDir(dir string) error {
	matches, err := filepath.Glob(filepath.Join(dir, "pw-*"))
	if err != nil {
		return err
	}
	var errs []error
	for _, m := range matches {
		errs = append(errs, os.Remove(m))
	}
	return errors.Join(errs...)
}
