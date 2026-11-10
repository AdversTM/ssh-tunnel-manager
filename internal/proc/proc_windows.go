package proc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Process is a hidden child process placed in its own Job Object.
// Killing it terminates the whole tree (e.g. ssh.exe + askpass helper); if the
// app dies, the job handle closes and Windows kills the tree too.
type Process struct {
	cmd   *exec.Cmd
	lines chan string
	done  chan struct{}
	err   error

	mu        sync.Mutex
	job       windows.Handle
	jobClosed bool
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return 0, errors.Join(fmt.Errorf("SetInformationJobObject: %w", err), windows.CloseHandle(job))
	}
	return job, nil
}

// Start launches exe without a console window. Stderr is delivered line by line
// on Lines(); stdout and stdin are the null device.
func Start(exe string, args, env []string) (*Process, error) {
	job, err := newKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, errors.Join(err, windows.CloseHandle(job))
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(fmt.Errorf("start %s: %w", exe, err), windows.CloseHandle(job))
	}
	ph, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("OpenProcess: %w", err), cmd.Process.Kill(), cmd.Wait(), windows.CloseHandle(job))
	}
	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		return nil, errors.Join(fmt.Errorf("AssignProcessToJobObject: %w", err), cmd.Process.Kill(), cmd.Wait(),
			windows.CloseHandle(ph), windows.CloseHandle(job))
	}

	p := &Process{cmd: cmd, job: job, lines: make(chan string, 256), done: make(chan struct{})}
	mainExited := make(chan struct{})

	// When the main process exits, terminate leftovers in the job so they cannot
	// keep the stderr pipe open and block the reader below.
	go func() {
		defer close(mainExited)
		defer windows.CloseHandle(ph)
		if _, err := windows.WaitForSingleObject(ph, windows.INFINITE); err != nil {
			return
		}
		if err := p.terminate(); err != nil {
			p.lines <- "terminate leftover processes: " + err.Error()
		}
	}()

	go func() {
		r := bufio.NewReader(stderr)
		for {
			line, err := r.ReadString('\n')
			if line = strings.TrimRight(line, "\r\n"); line != "" {
				p.lines <- line
			}
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
					p.lines <- "stderr read error: " + err.Error()
				}
				break
			}
		}
		<-mainExited
		close(p.lines)
		p.err = cmd.Wait()
		p.closeJob()
		close(p.done)
	}()
	return p, nil
}

func (p *Process) terminate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.jobClosed {
		return nil
	}
	if err := windows.TerminateJobObject(p.job, 1); err != nil {
		return fmt.Errorf("TerminateJobObject: %w", err)
	}
	return nil
}

func (p *Process) closeJob() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.jobClosed {
		p.jobClosed = true
		windows.CloseHandle(p.job)
	}
}

func (p *Process) Lines() <-chan string  { return p.lines }
func (p *Process) Done() <-chan struct{} { return p.done }
func (p *Process) PID() int              { return p.cmd.Process.Pid }

// Err returns the wait result; valid after Done() is closed.
func (p *Process) Err() error { return p.err }

// Kill terminates the whole process tree. Safe to call after exit.
func (p *Process) Kill() error { return p.terminate() }
