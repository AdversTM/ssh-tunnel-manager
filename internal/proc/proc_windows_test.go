package proc

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if os.Getenv("PROC_TEST_HELPER") == "1" {
		p, err := Start("ping", []string{"-n", "120", "127.0.0.1"}, nil)
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		fmt.Println(p.PID())
		<-p.Done()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func collect(t *testing.T, p *Process, timeout time.Duration) []string {
	t.Helper()
	var out []string
	deadline := time.After(timeout)
	for {
		select {
		case l, ok := <-p.Lines():
			if !ok {
				select {
				case <-p.Done():
				case <-deadline:
					t.Fatal("process not done after lines closed")
				}
				return out
			}
			out = append(out, l)
		case <-deadline:
			t.Fatalf("timeout; lines so far %v", out)
		}
	}
}

func waitGone(pid int, timeout time.Duration) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
	return err == nil && ev == windows.WAIT_OBJECT_0
}

func childPIDs(parent int) ([]int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var pids []int
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ParentProcessID) == parent {
			pids = append(pids, int(e.ProcessID))
		}
	}
	return pids, nil
}

func TestStderrLines(t *testing.T) {
	p, err := Start("cmd", []string{"/c", "echo one 1>&2 & echo two 1>&2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, p, 10*time.Second)
	if len(got) != 2 || strings.TrimSpace(got[0]) != "one" || strings.TrimSpace(got[1]) != "two" {
		t.Fatalf("lines=%q", got)
	}
	if p.Err() != nil {
		t.Fatalf("exit err %v", p.Err())
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("kill after exit must be a no-op: %v", err)
	}
}

func TestEnvIsPassed(t *testing.T) {
	p, err := Start("cmd", []string{"/c", "echo %PROC_TEST_VAR% 1>&2"}, []string{"PROC_TEST_VAR=hello"})
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, p, 10*time.Second)
	if len(got) != 1 || strings.TrimSpace(got[0]) != "hello" {
		t.Fatalf("lines=%q", got)
	}
}

func TestKillTerminatesTree(t *testing.T) {
	p, err := Start("cmd", []string{"/c", "ping -n 120 127.0.0.1 >NUL"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var kids []int
	for i := 0; i < 50 && len(kids) == 0; i++ {
		time.Sleep(100 * time.Millisecond)
		if kids, err = childPIDs(p.PID()); err != nil {
			t.Fatal(err)
		}
	}
	if len(kids) == 0 {
		t.Fatal("grandchild ping not found")
	}
	start := time.Now()
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	collect(t, p, 5*time.Second)
	for _, k := range kids {
		if !waitGone(k, 2*time.Second) {
			t.Fatalf("grandchild %d still alive", k)
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("kill took %v", d)
	}
}

func TestChildDiesWithParent(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(self, "-test.run=^$")
	helper.Env = append(os.Environ(), "PROC_TEST_HELPER=1")
	out, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("read helper output: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("helper said %q", line)
	}
	if waitGone(childPID, 0) {
		t.Fatal("child not running before parent kill")
	}
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := helper.Wait(); err == nil {
		t.Fatal("expected helper to be killed")
	}
	if !waitGone(childPID, 2*time.Second) {
		t.Fatalf("child %d survived parent kill", childPID)
	}
}
