package winsys

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NetworkFingerprint describes the adapters that can reach other networks:
// operational adapters with a default gateway, with their addresses. Virtual
// switches (Docker, Hyper-V, WSL) have no gateway and do not affect it.
func NetworkFingerprint() (string, error) {
	size := uint32(15 << 10)
	var buf []byte
	for range 3 {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_GATEWAYS,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("GetAdaptersAddresses: %w", err)
		}
		var parts []string
		for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
			if a.OperStatus != windows.IfOperStatusUp || a.FirstGatewayAddress == nil {
				continue
			}
			var addrs []string
			for u := a.FirstUnicastAddress; u != nil; u = u.Next {
				if ip := u.Address.IP(); ip != nil && !ip.IsLinkLocalUnicast() {
					addrs = append(addrs, ip.String())
				}
			}
			for g := a.FirstGatewayAddress; g != nil; g = g.Next {
				if ip := g.Address.IP(); ip != nil {
					addrs = append(addrs, "gw="+ip.String())
				}
			}
			sort.Strings(addrs)
			parts = append(parts, windows.UTF16PtrToString(a.FriendlyName)+"="+strings.Join(addrs, ","))
		}
		sort.Strings(parts)
		return strings.Join(parts, ";"), nil
	}
	return "", errors.New("GetAdaptersAddresses: buffer kept growing")
}

// netWatcher debounces interface notifications and reports only real changes
// of the fingerprint.
type netWatcher struct {
	debounce    time.Duration
	fingerprint func() (string, error)
	onChange    func(online bool)
	onError     func(error)

	mu    sync.Mutex
	last  string
	timer *time.Timer
}

func (w *netWatcher) trigger() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(w.debounce, w.check)
}

func (w *netWatcher) check() {
	fp, err := w.fingerprint()
	if err != nil {
		w.onError(err)
		return
	}
	w.mu.Lock()
	changed := fp != w.last
	w.last = fp
	w.mu.Unlock()
	if changed {
		w.onChange(fp != "")
	}
}

func (w *netWatcher) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
}

var (
	netOnce     sync.Once
	netCallback uintptr
	netMu       sync.Mutex
	netWatchers = map[int]*netWatcher{}
	netNextID   int
)

func netDispatch(_, _, _ uintptr) uintptr {
	netMu.Lock()
	ws := make([]*netWatcher, 0, len(netWatchers))
	for _, w := range netWatchers {
		ws = append(ws, w)
	}
	netMu.Unlock()
	for _, w := range ws {
		w.trigger()
	}
	return 0
}

// WatchNetwork calls onChange after interface changes have settled for
// debounce, when NetworkFingerprint differs from the previous one; online
// reports whether any gateway adapter is up.
func WatchNetwork(debounce time.Duration, onChange func(online bool), onError func(error)) (stop func() error, err error) {
	initial, err := NetworkFingerprint()
	if err != nil {
		return nil, err
	}
	w := &netWatcher{debounce: debounce, fingerprint: NetworkFingerprint, onChange: onChange, onError: onError, last: initial}
	netOnce.Do(func() { netCallback = windows.NewCallback(netDispatch) })
	netMu.Lock()
	netNextID++
	id := netNextID
	netWatchers[id] = w
	netMu.Unlock()

	var h windows.Handle
	if err := windows.NotifyIpInterfaceChange(windows.AF_UNSPEC, netCallback, nil, false, &h); err != nil {
		netMu.Lock()
		delete(netWatchers, id)
		netMu.Unlock()
		return nil, fmt.Errorf("NotifyIpInterfaceChange: %w", err)
	}
	return func() error {
		err := windows.CancelMibChangeNotify2(h)
		netMu.Lock()
		delete(netWatchers, id)
		netMu.Unlock()
		w.stop()
		if err != nil {
			return fmt.Errorf("CancelMibChangeNotify2: %w", err)
		}
		return nil
	}, nil
}
