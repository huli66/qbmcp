//go:build windows

package tests

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"qbmcp/internal/config"
	"qbmcp/internal/fileutil"
	"qbmcp/internal/host"
)

// This opt-in test creates a real per-user task under an isolated home, runs
// actual crash recovery, and removes its task/PATH entry in cleanup. It does
// not alter the default qbmcp profile.
func TestUserTaskLifecycle(t *testing.T) {
	if os.Getenv("QBMCP_LIFECYCLE_TEST") != "1" {
		t.Skip("set QBMCP_LIFECYCLE_TEST=1 and build dist/qbmcp.exe to test Task Scheduler")
	}
	exe, err := filepath.Abs(filepath.Join("..", "dist", "qbmcp.exe"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "测试 home's data")
	t.Setenv("QBMCP_HOME", home)
	activeExe := exe
	if previous := os.Getenv("QBMCP_PREVIOUS_BINARY"); previous != "" {
		activeExe, err = filepath.Abs(previous)
		if err != nil {
			t.Fatal(err)
		}
	}
	runAt := func(program string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, program, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		out, e := cmd.CombinedOutput()
		return string(out), e
	}
	run := func(args ...string) (string, error) { return runAt(activeExe, args...) }
	command := func(args ...string) {
		t.Helper()
		out, e := run(args...)
		if e != nil {
			t.Fatalf("%v: %v\n%s", args, e, out)
		}
	}
	oldPath := func() string {
		k, e := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
		if e != nil {
			t.Fatal(e)
		}
		defer k.Close()
		v, _, e := k.GetStringValue("Path")
		if e != nil && !errors.Is(e, registry.ErrNotExist) {
			t.Fatal(e)
		}
		return v
	}()
	t.Cleanup(func() {
		out, e := run("uninstall")
		if e != nil {
			t.Errorf("cleanup: %v %s", e, out)
		}
	})
	t.Log("Installing a separate per-user task; elevated:", windows.GetCurrentProcessToken().IsElevated())
	command("install")
	command("install")
	task, err := queryTask()
	if err != nil || !task.Installed || task.Autostart {
		t.Fatalf("initial task: %+v %v", task, err)
	}
	if !task.WatchEnabled || !strings.EqualFold(task.Executable, host.BackgroundPath()) {
		t.Fatalf("task must run the native background executable directly: %+v", task)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	// Edit only supported values while retaining any credentials an old build needs.
	configBytes, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	var previousConfig map[string]any
	if err := json.Unmarshal(configBytes, &previousConfig); err != nil {
		t.Fatal(err)
	}
	previousConfig["allowed_origins"] = []string{"http://localhost:5173"}
	if err := fileutil.WriteJSON(config.Path(), previousConfig); err != nil {
		t.Fatal(err)
	}
	command("start", "--port", strconv.Itoa(port))
	state, err := host.ReadRuntime()
	if err != nil || !host.RuntimeAlive(state) {
		t.Fatalf("runtime: %+v %v", state, err)
	}
	command("enable")
	command("stop")
	t.Log("Installing the release archive over the stopped test instance")
	packageDir := unpackRelease(t)
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if out, err := runAt(powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(packageDir, "install.ps1")); err != nil {
		t.Fatalf("packaged installer: %v\n%s", err, out)
	}
	activeExe = filepath.Join(config.InstallDir(), "qbmcp.exe")
	expected, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(activeExe)
	if err != nil || !bytes.Equal(expected, installed) {
		t.Fatalf("CLI was not replaced: %v", err)
	}
	expectedBackground, err := host.BackgroundImage(expected)
	if err != nil {
		t.Fatal(err)
	}
	installedBackground, err := os.ReadFile(host.BackgroundPath())
	if err != nil || !bytes.Equal(expectedBackground, installedBackground) {
		t.Fatalf("background image was not replaced: %v", err)
	}
	updatedConfig, err := config.Load()
	if err != nil || updatedConfig.Port != port || len(updatedConfig.AllowedOrigins) != 1 || updatedConfig.AllowedOrigins[0] != "http://localhost:5173" {
		t.Fatalf("upgrade lost settings: %+v %v", updatedConfig, err)
	}
	task, err = queryTask()
	if err != nil || !task.Autostart {
		t.Fatalf("upgrade lost login startup: %+v %v", task, err)
	}
	command("start")
	state, err = host.ReadRuntime()
	if err != nil || !host.RuntimeAlive(state) {
		t.Fatalf("upgraded runtime: %+v %v", state, err)
	}
	upgradedHealth, err := host.QueryHealth(updatedConfig, state.PID)
	if err != nil || upgradedHealth.Version != config.Version {
		t.Fatalf("wrong running version: %+v %v", upgradedHealth, err)
	}
	t.Log("Packaged overwrite passed: binaries, version, port, origins, and login startup preserved")
	firstPID := state.PID
	if state.ConsoleAttached {
		t.Fatal("scheduled worker allocated a console")
	}
	status, err := host.QueryStatus()
	if err != nil || status.Port != port || status.Health == nil || status.PortSource != "runtime" {
		t.Fatalf("running status: %+v %v", status, err)
	}
	command("start")
	command("enable")
	task, err = queryTask()
	if err != nil || !task.Autostart {
		t.Fatalf("enable: %+v %v", task, err)
	}
	command("disable")
	state, _ = host.ReadRuntime()
	if state.PID != firstPID || !host.RuntimeAlive(state) {
		t.Fatal("enable/disable restarted the worker")
	}
	if _, err = run("start", "--port", strconv.Itoa(port+1)); err == nil {
		t.Fatal("changed port while running")
	}
	t.Log("Killing the test worker to verify real Task Scheduler recovery (~60 seconds)")
	process, err := os.FindProcess(firstPID)
	if err != nil {
		t.Fatal(err)
	}
	if err = process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		state, _ = host.ReadRuntime()
		if state.PID != firstPID && state.State == "running" && host.RuntimeAlive(state) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if state.PID == firstPID || !host.RuntimeAlive(state) {
		task, taskErr := queryTask()
		t.Logf("recovery diagnostic runtime=%+v task=%+v taskError=%v", state, task, taskErr)
		if logs, e := os.ReadFile(filepath.Join(config.DataDir(), "logs", "qbmcp.log")); e == nil {
			t.Log(string(logs))
		}
		t.Fatal("worker did not recover")
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.QueryHealth(c, state.PID); err != nil {
		t.Fatal(err)
	}
	t.Log("Crash recovery passed; verifying deliberate stop is not restarted")
	command("stop")
	time.Sleep(65 * time.Second)
	state, _ = host.ReadRuntime()
	if host.RuntimeAlive(state) {
		t.Fatal("manual stop restarted")
	}
	status, err = host.QueryStatus()
	if err != nil || status.State != "stopped" || status.Port != port || status.Health != nil {
		t.Fatalf("stopped status lost configuration: %+v %v", status, err)
	}
	command("start")
	command("stop")
	// A deterministic listen failure must be surfaced and not loop forever.
	occupied, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run("start"); err == nil {
		occupied.Close()
		t.Fatal("occupied port accepted")
	}
	occupied.Close()
	state, _ = host.ReadRuntime()
	if state.State != "failed" {
		t.Fatalf("failed startup state: %+v", state)
	}
	command("uninstall")
	task, err = queryTask()
	if err != nil || task.Installed {
		t.Fatal("task not removed")
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	newPath, _, _ := k.GetStringValue("Path")
	if newPath != oldPath {
		t.Fatal("original PATH was not restored")
	}
}

func queryTask() (host.TaskStatus, error) {
	status, err := host.QueryStatus()
	return status.Task, err
}

func unpackRelease(t *testing.T) string {
	t.Helper()
	name := "qbmcp_" + config.Version + "_windows_" + runtime.GOARCH + ".zip"
	archive, err := zip.OpenReader(filepath.Join("..", "dist", name))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	destination := t.TempDir()
	for _, entry := range archive.File {
		if !filepath.IsLocal(entry.Name) {
			t.Fatalf("unsafe archive path: %s", entry.Name)
		}
		path := filepath.Join(destination, entry.Name)
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return destination
}
