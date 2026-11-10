package hostkey

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sshExe = `C:\Windows\System32\OpenSSH\ssh.exe`

// Prompt captured from OpenSSH_for_Windows_9.5p1 askpass.
const prompt = "The authenticity of host '[127.0.0.1]:2222 ([127.0.0.1]:2222)' can't be established.\n" +
	"ED25519 key fingerprint is SHA256:xKBMZBKqT6MDy9DjELSmt7wcVR2zWOCIDNVfONO0tLE.\n" +
	"This key is not known by any other names.\n" +
	"Are you sure you want to continue connecting (yes/no/[fingerprint])? "

func TestParsePrompt(t *testing.T) {
	if !IsPrompt(prompt) {
		t.Fatal("IsPrompt=false")
	}
	if IsPrompt("pwuser@127.0.0.1's password: ") {
		t.Fatal("password prompt detected as host key prompt")
	}
	got, ok := ParsePrompt(prompt)
	want := PromptInfo{Host: "[127.0.0.1]:2222", KeyType: "ED25519", Fingerprint: "SHA256:xKBMZBKqT6MDy9DjELSmt7wcVR2zWOCIDNVfONO0tLE"}
	if !ok || got != want {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestKnownHostName(t *testing.T) {
	if KnownHostName("vps", 22) != "vps" || KnownHostName("1.2.3.4", 2222) != "[1.2.3.4]:2222" {
		t.Fatal("bad name")
	}
}

func TestKnownFingerprintAndRemove(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "hk")
	if out, err := exec.Command(filepath.Join(filepath.Dir(sshExe), "ssh-keygen.exe"),
		"-q", "-t", "ed25519", "-N", "", "-f", keyFile).CombinedOutput(); err != nil {
		t.Fatalf("keygen: %v %s", err, out)
	}
	pub, err := os.ReadFile(keyFile + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(pub))
	kh := filepath.Join(dir, "known_hosts")
	entries := "[127.0.0.1]:2222 " + fields[0] + " " + fields[1] + "\nother " + fields[0] + " " + fields[1] + "\n"
	if err := os.WriteFile(kh, []byte(entries), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join(filepath.Dir(sshExe), "ssh-keygen.exe"), "-lf", keyFile+".pub").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(out))[1]

	got, err := KnownFingerprint(sshExe, kh, "127.0.0.1", 2222)
	if err != nil || got != want {
		t.Fatalf("got %q want %q err %v", got, want, err)
	}
	if got, err := KnownFingerprint(sshExe, kh, "127.0.0.1", 2223); err != nil || got != "" {
		t.Fatalf("unknown host: %q %v", got, err)
	}
	if got, err := KnownFingerprint(sshExe, filepath.Join(dir, "none"), "x", 22); err != nil || got != "" {
		t.Fatalf("missing file: %q %v", got, err)
	}

	if err := Remove(sshExe, kh, "127.0.0.1", 2222); err != nil {
		t.Fatal(err)
	}
	if got, _ := KnownFingerprint(sshExe, kh, "127.0.0.1", 2222); got != "" {
		t.Fatal("entry not removed")
	}
	if got, _ := KnownFingerprint(sshExe, kh, "other", 22); got != want {
		t.Fatal("unrelated entry removed")
	}
	if _, err := os.Stat(kh + ".old"); !os.IsNotExist(err) {
		t.Fatal("backup file left behind")
	}
}
