package winsys

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	powrprof       = windows.NewLazySystemDLL("powrprof.dll")
	procRegister   = powrprof.NewProc("PowerRegisterSuspendResumeNotification")
	procUnregister = powrprof.NewProc("PowerUnregisterSuspendResumeNotification")
)

const (
	deviceNotifyCallback  = 2
	pbtAPMResumeSuspend   = 0x7
	pbtAPMResumeAutomatic = 0x12
)

type deviceNotifySubscribeParameters struct {
	callback uintptr
	context  uintptr
}

// resumeFilter collapses the two resume events Windows sends per wake-up
// (PBT_APMRESUMEAUTOMATIC, then PBT_APMRESUMESUSPEND) into one call.
type resumeFilter struct {
	mu       sync.Mutex
	last     time.Time
	window   time.Duration
	onResume func()
}

func (f *resumeFilter) event(typ uint32, now time.Time) {
	if typ != pbtAPMResumeAutomatic && typ != pbtAPMResumeSuspend {
		return
	}
	f.mu.Lock()
	dup := !f.last.IsZero() && now.Sub(f.last) < f.window
	if !dup {
		f.last = now
	}
	f.mu.Unlock()
	if !dup {
		go f.onResume()
	}
}

var (
	powerOnce     sync.Once
	powerCallback uintptr
	powerMu       sync.Mutex
	powerFilters  = map[uintptr]*resumeFilter{}
	powerNextID   uintptr
)

func powerDispatch(context, typ, _ uintptr) uintptr {
	powerMu.Lock()
	f := powerFilters[context]
	powerMu.Unlock()
	if f != nil {
		f.event(uint32(typ), time.Now())
	}
	return 0
}

// WatchResume calls onResume (on its own goroutine) once per wake-up from sleep or hibernation.
func WatchResume(onResume func()) (stop func() error, err error) {
	powerOnce.Do(func() { powerCallback = windows.NewCallback(powerDispatch) })
	powerMu.Lock()
	powerNextID++
	id := powerNextID
	powerFilters[id] = &resumeFilter{window: 30 * time.Second, onResume: onResume}
	powerMu.Unlock()

	params := &deviceNotifySubscribeParameters{callback: powerCallback, context: id}
	var handle uintptr
	r, _, _ := procRegister.Call(deviceNotifyCallback, uintptr(unsafe.Pointer(params)), uintptr(unsafe.Pointer(&handle)))
	if r != 0 {
		powerMu.Lock()
		delete(powerFilters, id)
		powerMu.Unlock()
		return nil, fmt.Errorf("PowerRegisterSuspendResumeNotification: %w", windows.Errno(r))
	}
	return func() error {
		r, _, _ := procUnregister.Call(handle)
		powerMu.Lock()
		delete(powerFilters, id)
		powerMu.Unlock()
		if r != 0 {
			return fmt.Errorf("PowerUnregisterSuspendResumeNotification: %w", windows.Errno(r))
		}
		return nil
	}, nil
}
