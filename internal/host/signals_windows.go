//go:build windows

package host

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func objectName(kind string) (string, error) {
	id, err := identity()
	return `Global\qbmcp-` + kind + "-" + id, err
}

func userSecurity() (*windows.SecurityAttributes, error) {
	sid, err := currentSID()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

var ErrAlreadyRunning = errors.New("后台程序已在运行")

func Lock(kind string, timeout uint32) (func(), error) {
	name, err := objectName(kind)
	if err != nil {
		return nil, err
	}
	ptr, _ := windows.UTF16PtrFromString(name)
	sa, err := userSecurity()
	if err != nil {
		return nil, err
	}
	// Win32 mutex ownership is attached to an OS thread, not a Go goroutine.
	runtime.LockOSThread()
	h, err := windows.CreateMutex(sa, false, ptr)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		runtime.UnlockOSThread()
		return nil, err
	}
	state, err := windows.WaitForSingleObject(h, timeout)
	if err != nil || (state != windows.WAIT_OBJECT_0 && state != windows.WAIT_ABANDONED) {
		windows.CloseHandle(h)
		runtime.UnlockOSThread()
		if err != nil {
			return nil, err
		}
		return nil, ErrAlreadyRunning
	}
	return func() { windows.ReleaseMutex(h); windows.CloseHandle(h); runtime.UnlockOSThread() }, nil
}

func stopEvent(create bool) (windows.Handle, error) {
	name, err := objectName("stop")
	if err != nil {
		return 0, err
	}
	ptr, _ := windows.UTF16PtrFromString(name)
	if !create {
		return windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, ptr)
	}
	sa, err := userSecurity()
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateEvent(sa, 1, 0, ptr)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		err = nil
	}
	return h, err
}

func signalStop() error {
	h, err := stopEvent(false)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.SetEvent(h)
}
