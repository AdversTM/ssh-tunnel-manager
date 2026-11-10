package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func cmdkeyList(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("cmdkey", "/list").CombinedOutput()
	if err != nil {
		t.Fatalf("cmdkey: %v %s", err, out)
	}
	return string(out)
}

func TestWinCredRoundTrip(t *testing.T) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	ref := "ssh-tunnel-manager:test-" + hex.EncodeToString(b)
	s := WinCred{}
	t.Cleanup(func() {
		if err := s.Delete(ref); err != nil {
			t.Error(err)
		}
	})

	if _, err := s.Get(ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.Set(ref, "pässwörd 1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ref)
	if err != nil || got != "pässwörd 1" {
		t.Fatalf("got %q %v", got, err)
	}
	if !strings.Contains(cmdkeyList(t), ref) {
		t.Fatal("cmdkey /list does not show the credential")
	}
	if err := s.Set(ref, "second"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ref); got != "second" {
		t.Fatalf("overwrite failed: %q", got)
	}
	if err := s.Delete(ref); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cmdkeyList(t), ref) {
		t.Fatal("credential still listed after delete")
	}
	if err := s.Delete(ref); err != nil {
		t.Fatalf("deleting missing credential must be a no-op: %v", err)
	}
}
