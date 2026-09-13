//go:build windows

package host

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"gopkg.in/natefinch/lumberjack.v2"

	"qbmcp/internal/config"
	"qbmcp/internal/server"
)

func RunBackground() error {
	unlock, err := Lock("runtime", 0)
	if errors.Is(err, ErrAlreadyRunning) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	if err = protectUserDir(config.DataDir()); err != nil {
		return err
	}
	if err = protectUserDir(filepath.Join(config.DataDir(), "logs")); err != nil {
		return err
	}
	writer := &lumberjack.Logger{Filename: filepath.Join(config.DataDir(), "logs", "qbmcp.log"), MaxSize: 5, MaxBackups: 3, MaxAge: 14, Compress: true}
	defer writer.Close()
	logger := slog.New(slog.NewJSONHandler(writer, nil))
	stamp, err := ProcessStart(os.Getpid())
	if err != nil {
		return err
	}
	console, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	state := RuntimeStatus{State: "starting", PID: os.Getpid(), ProcessStart: stamp, ConsoleAttached: console != 0}
	startupFailed := func(err error) error {
		state.State = "failed"
		state.Error = err.Error()
		logger.Error("startup failed", "error", err)
		if flagErr := SetBootFlag("failed-boot", true); flagErr != nil {
			return flagErr
		}
		// Configuration/listen failures are deterministic: exit successfully to
		// suppress scheduler retries, but retain the failure for start/status.
		return writeRuntime(state)
	}
	stopped, err := StoppedThisBoot()
	if err != nil {
		return startupFailed(err)
	}
	if stopped {
		state.State = "stopped"
		return writeRuntime(state)
	}
	if !BootFlag("desired-boot") || BootFlag("failed-boot") {
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
	stopped, err = StoppedThisBoot()
	if err != nil {
		return startupFailed(err)
	}
	if stopped {
		state.State = "stopped"
		return writeRuntime(state)
	}
	c, err := config.Load()
	if err != nil {
		return startupFailed(err)
	}
	state.Port = c.Port
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.Port))
	if err != nil {
		return startupFailed(err)
	}
	defer listener.Close()
	a := server.New(c, logger)
	server := &http.Server{Handler: a, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16384}
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
