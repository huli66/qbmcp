//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestUserPathsAndConfig(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	c, err := prepareConfig(34567)
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 34567 || !strings.HasPrefix(configPath(), dataDir()) {
		t.Fatal("unexpected config location")
	}
	updated, err := prepareConfig(34568)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Port != 34568 || updated.Token != c.Token {
		t.Fatal("port update changed token")
	}
	if installDir() != filepath.Join(dataDir(), "bin") {
		t.Fatal("isolated install directory mismatch")
	}
}
func TestPathPreservesUnrelatedEntries(t *testing.T) {
	entry := `C:\Users\测试\Programs\qbmcp`
	for _, old := range []string{"", `C:\Windows;C:\工具`, `%SystemRoot%\System32;`, `C:\one;;C:\two`} {
		added := editPath(old, entry, true)
		if editPath(added, entry, true) != added {
			t.Fatal("PATH install is not idempotent")
		}
		if editPath(added, entry, false) != old {
			t.Fatalf("uninstall changed original PATH %q", old)
		}
	}
	if got := editPath(`C:\before;"C:\Users\测试\Programs\qbmcp\";C:\after`, entry, false); got != `C:\before;C:\after` {
		t.Fatal(got)
	}
}
func TestManagementMutexSerializesCommands(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	release, err := lockNamed("test", 0)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		other, e := lockNamed("test", 30)
		if e == nil {
			other()
		}
		result <- e
	}()
	if err = <-result; !errors.Is(err, errAlreadyRunning) {
		t.Fatalf("concurrent lock: %v", err)
	}
	release()
	release, err = lockNamed("test", 30)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestProcessIdentityRejectsReusedPID(t *testing.T) {
	stamp, err := processStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !runtimeAlive(RuntimeStatus{PID: os.Getpid(), ProcessStart: stamp}) {
		t.Fatal("own process not alive")
	}
	if runtimeAlive(RuntimeStatus{PID: os.Getpid(), ProcessStart: stamp + 1}) {
		t.Fatal("reused PID accepted")
	}
}

// This opt-in test creates a real per-user task under an isolated home, runs
// actual crash recovery, and removes its task/PATH entry in cleanup. It does
// not alter the default qbmcp profile.
func TestUserTaskLifecycle(t *testing.T) {
	if os.Getenv("QBMCP_LIFECYCLE_TEST") != "1" {
		t.Skip("set QBMCP_LIFECYCLE_TEST=1 and build dist/qbmcp.exe to test Task Scheduler")
	}
	exe, err := filepath.Abs(filepath.Join("dist", "qbmcp.exe"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "测试 home's data")
	t.Setenv("QBMCP_HOME", home)
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		out, e := cmd.CombinedOutput()
		return string(out), e
	}
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
	task, err := scheduler("query", nil)
	if err != nil || !task.Installed || task.Autostart {
		t.Fatalf("initial task: %+v %v", task, err)
	}
	if !task.WatchEnabled || !strings.EqualFold(task.Executable, backgroundPath()) {
		t.Fatalf("task must run the native background executable directly: %+v", task)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	command("start", "--port", fmtInt(port))
	state, err := readRuntime()
	if err != nil || !runtimeAlive(state) {
		t.Fatalf("runtime: %+v %v", state, err)
	}
	firstPID := state.PID
	if state.ConsoleAttached {
		t.Fatal("scheduled worker allocated a console")
	}
	status, err := queryUserStatus()
	if err != nil || status.Port != port || status.Health == nil || status.PortSource != "runtime" {
		t.Fatalf("running status: %+v %v", status, err)
	}
	command("start")
	command("enable")
	task, err = scheduler("query", nil)
	if err != nil || !task.Autostart {
		t.Fatalf("enable: %+v %v", task, err)
	}
	command("disable")
	state, _ = readRuntime()
	if state.PID != firstPID || !runtimeAlive(state) {
		t.Fatal("enable/disable restarted the worker")
	}
	if _, err = run("start", "--port", fmtInt(port+1)); err == nil {
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
		state, _ = readRuntime()
		if state.PID != firstPID && state.State == "running" && runtimeAlive(state) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if state.PID == firstPID || !runtimeAlive(state) {
		task, taskErr := scheduler("query", nil)
		t.Logf("recovery diagnostic runtime=%+v task=%+v taskError=%v", state, task, taskErr)
		if logs, e := os.ReadFile(filepath.Join(dataDir(), "logs", "qbmcp.log")); e == nil {
			t.Log(string(logs))
		}
		t.Fatal("worker did not recover")
	}
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = getHealth(c, state.PID); err != nil {
		t.Fatal(err)
	}
	t.Log("Crash recovery passed; verifying deliberate stop is not restarted")
	command("stop")
	time.Sleep(65 * time.Second)
	state, _ = readRuntime()
	if runtimeAlive(state) {
		t.Fatal("manual stop restarted")
	}
	status, err = queryUserStatus()
	if err != nil || status.State != "stopped" || status.Port != port || status.Health != nil {
		t.Fatalf("stopped status lost configuration: %+v %v", status, err)
	}
	command("start")
	command("stop")
	// A deterministic listen failure must be surfaced and not loop forever.
	occupied, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtInt(port)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run("start"); err == nil {
		occupied.Close()
		t.Fatal("occupied port accepted")
	}
	occupied.Close()
	state, _ = readRuntime()
	if state.State != "failed" {
		t.Fatalf("failed startup state: %+v", state)
	}
	command("uninstall")
	task, err = scheduler("query", nil)
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

func fmtInt(n int) string { return fmt.Sprintf("%d", n) }
