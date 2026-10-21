//go:build windows

package platform

import (
	"context"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	powrprof                    = windows.NewLazySystemDLL("powrprof.dll")
	procRegisterSuspendResume   = powrprof.NewProc("PowerRegisterSuspendResumeNotification")
	procUnregisterSuspendResume = powrprof.NewProc("PowerUnregisterSuspendResumeNotification")
)

const (
	// DEVICE_NOTIFY_CALLBACK tells powrprof.dll to deliver notifications
	// through the DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS struct below. The
	// sibling value DEVICE_NOTIFY_SERVICE_HANDLE (1) is for service handles
	// and makes the registration fail when a callback struct is supplied,
	// which is why the watcher could never arm on Windows.
	deviceNotifyCallback = 2

	// Power broadcast types from winuser.h / powrprof.h.
	pbtAPMSuspend       = 0x0004
	pbtAPMResumeSuspend = 0x0007
)

// deviceNotifySubscribeParameters mirrors the Win32
// DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS struct.
type deviceNotifySubscribeParameters struct {
	callback uintptr
	context  uintptr
}

// windowsSleepEvent is the shared delivery channel between the Windows
// power callback and the goroutine blocked in Watch. A single global
// channel is sufficient because the service hosts exactly one sleep
// source; the mutex serializes registration/unregistration.
var (
	sleepMu       sync.Mutex
	sleepDelivery chan<- SleepEvent
	sleepCtx      context.Context
)

// powerSuspendResumeCallback is the stdcall function invoked by
// powrprof.dll on a Windows power-event thread. NewCallback pins the
// Go function for the process lifetime (one registration).
var powerSuspendResumeCallback = windows.NewCallback(func(handle uintptr, typ uint32, ctx uintptr) uintptr {
	var event SleepEvent
	switch typ {
	case pbtAPMSuspend:
		event = SleepEvent{Preparing: true}
	case pbtAPMResumeSuspend:
		event = SleepEvent{Preparing: false}
	default:
		return 0
	}
	sleepMu.Lock()
	ch := sleepDelivery
	c := sleepCtx
	sleepMu.Unlock()
	if ch == nil {
		return 0
	}
	select {
	case ch <- event:
	case <-c.Done():
	}
	return 0
})

type windowsSleepSource struct{}

func DefaultSleepSource() SleepSource { return windowsSleepSource{} }

// Watch registers for native Windows suspend/resume notifications via
// powrprof.dll PowerRegisterSuspendResumeNotification and blocks until
// the context is canceled. Each suspend/resume cycle produces one
// Preparing=true event followed by one Preparing=false event, matching
// the Linux logind and Darwin powerd semantics exactly.
func (windowsSleepSource) Watch(ctx context.Context, out chan<- SleepEvent) error {
	sleepMu.Lock()
	sleepDelivery = out
	sleepCtx = ctx
	sleepMu.Unlock()

	params := deviceNotifySubscribeParameters{
		callback: powerSuspendResumeCallback,
		context:  0,
	}
	var handle uintptr
	ret, _, err := procRegisterSuspendResume.Call(
		uintptr(deviceNotifyCallback),
		uintptr(unsafe.Pointer(&params)),
		uintptr(unsafe.Pointer(&handle)),
	)
	if ret != 0 {
		sleepMu.Lock()
		sleepDelivery = nil
		sleepCtx = nil
		sleepMu.Unlock()
		// ret is the Win32 status; err is only GetLastError, which stays 0
		// on most power-API failures, so report both without dressing it up
		// as a wrapped error.
		return fmt.Errorf("register suspend/resume notification: win32 status %d (last error: %v)", ret, err)
	}

	<-ctx.Done()

	procUnregisterSuspendResume.Call(handle)
	sleepMu.Lock()
	sleepDelivery = nil
	sleepCtx = nil
	sleepMu.Unlock()
	return ctx.Err()
}
