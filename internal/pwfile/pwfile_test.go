package pwfile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCreateOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	path, remove, err := Create(dir, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "s3cret\n" {
		t.Fatalf("content %q %v", data, err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	wantSDDL, err := OwnerOnlySDDL()
	if err != nil {
		t.Fatal(err)
	}
	wantSD, err := windows.SecurityDescriptorFromString(wantSDDL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sd.String(), wantSD.String(); got != want {
		t.Fatalf("dacl %s want %s", got, want)
	}
	remove()
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file not removed: %v", err)
	}
}

func TestCleanDir(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, _, err := Create(dir, "x"); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(keep, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanDir(dir); err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadDir(dir)
	if err != nil || len(left) != 1 || left[0].Name() != "other.txt" {
		t.Fatalf("left %v %v", left, err)
	}
}
