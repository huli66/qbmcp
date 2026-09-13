//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"gopkg.in/natefinch/lumberjack.v2"
)

type RuntimeStatus struct {
	ConsoleAttached bool      `json:"console_attached"`
	State           string    `json:"state"`
	PID             int       `json:"pid"`
	ProcessStart    uint64    `json:"process_start"`
	Port            int       `json:"port"`
	Error           string    `json:"error,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func atomicJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "write-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func readRuntime() (RuntimeStatus, error) {
	var state RuntimeStatus
	b, err := os.ReadFile(filepath.Join(dataDir(), "runtime.json"))
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(b, &state)
	return state, err
}
func writeRuntime(state RuntimeStatus) error {
	state.UpdatedAt = time.Now().UTC()
	return atomicJSON(filepath.Join(dataDir(), "runtime.json"), state)
}
func processStart(pid int) (uint64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)
	state, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return 0, err
	}
	if state != uint32(windows.WAIT_TIMEOUT) {
		return 0, errors.New("process exited")
	}
	var start, end, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &start, &end, &kernel, &user); err != nil {
		return 0, err
	}
	return uint64(start.HighDateTime)<<32 | uint64(start.LowDateTime), nil
}
func runtimeAlive(state RuntimeStatus) bool {
	if state.PID <= 0 || state.ProcessStart == 0 {
		return false
	}
	start, err := processStart(state.PID)
	return err == nil && start == state.ProcessStart
}
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

var errAlreadyRunning = errors.New("后台程序已在运行")

func lockNamed(kind string, timeout uint32) (func(), error) {
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
		return nil, errAlreadyRunning
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
func runBackground() error {
	unlock, err := lockNamed("runtime", 0)
	if errors.Is(err, errAlreadyRunning) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	if err = protectUserDir(dataDir()); err != nil {
		return err
	}
	if err = protectUserDir(filepath.Join(dataDir(), "logs")); err != nil {
		return err
	}
	writer := &lumberjack.Logger{Filename: filepath.Join(dataDir(), "logs", "qbmcp.log"), MaxSize: 5, MaxBackups: 3, MaxAge: 14, Compress: true}
	defer writer.Close()
	logger := slog.New(slog.NewJSONHandler(writer, nil))
	stamp, err := processStart(os.Getpid())
	if err != nil {
		return err
	}
	console, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	state := RuntimeStatus{State: "starting", PID: os.Getpid(), ProcessStart: stamp, ConsoleAttached: console != 0}
	startupFailed := func(err error) error {
		state.State = "failed"
		state.Error = err.Error()
		logger.Error("startup failed", "error", err)
		if flagErr := setBootFlag("failed-boot", true); flagErr != nil {
			return flagErr
		}
		// Configuration/listen failures are deterministic: exit successfully to
		// suppress scheduler retries, but retain the failure for start/status.
		return writeRuntime(state)
	}
	stopped, err := stoppedThisBoot()
	if err != nil {
		return startupFailed(err)
	}
	if stopped {
		state.State = "stopped"
		return writeRuntime(state)
	}
	if !bootFlag("desired-boot") || bootFlag("failed-boot") {
		return nil
	}
	if err = writeRuntime(state); err != nil {
		return err
	}
	stop, err := stopEvent(true)
	if err != nil {
		return startupFailed(err)
	}
	defer windows.CloseHandle(stop)
	// Recheck after exposing the stop event to close the start/stop race.
	stopped, err = stoppedThisBoot()
	if err != nil {
		return startupFailed(err)
	}
	if stopped {
		state.State = "stopped"
		return writeRuntime(state)
	}
	c, err := loadConfig()
	if err != nil {
		return startupFailed(err)
	}
	state.Port = c.Port
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.Port))
	if err != nil {
		return startupFailed(err)
	}
	defer listener.Close()
	a := newApp(c, logger)
	server := &http.Server{Handler: a.handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	state.State = "running"
	if err = writeRuntime(state); err != nil {
		a.Close()
		server.Close()
		return err
	}
	logger.Info("background started", "port", c.Port, "pid", state.PID)
	stopResult := make(chan uint32, 1)
	go func() {
		which, e := windows.WaitForSingleObject(stop, windows.INFINITE)
		if e != nil {
			which = windows.WAIT_FAILED
		}
		stopResult <- which
	}()
	var runErr error
	watcherDone := false
	select {
	case runErr = <-done:
		if runErr == nil {
			runErr = errors.New("HTTP server exited unexpectedly")
		}
	case which := <-stopResult:
		watcherDone = true
		if which != windows.WAIT_OBJECT_0 {
			runErr = errors.New("stop event wait failed")
		}
	}
	// Also wakes the watcher if the HTTP server was the first to exit.
	_ = windows.SetEvent(stop)
	if !watcherDone {
		<-stopResult
	}
	a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	_ = server.Shutdown(ctx)
	cancel()
	_ = server.Close()
	if runErr != nil {
		state.State = "failed"
		state.Error = runErr.Error()
		logger.Error("background exited unexpectedly", "error", runErr)
	} else {
		state.State = "stopped"
		logger.Info("background stopped normally")
	}
	_ = writeRuntime(state)
	return runErr
}
