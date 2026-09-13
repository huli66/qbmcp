//go:build windows

package tests

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qbmcp/internal/config"
	"qbmcp/internal/host"
)

func TestUserPathsAndConfig(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	c, err := host.PrepareConfig(34567)
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 34567 || !strings.HasPrefix(config.Path(), config.DataDir()) {
		t.Fatal("unexpected config location")
	}
	updated, err := host.PrepareConfig(34568)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Port != 34568 {
		t.Fatal("port update did not persist")
	}
	if config.InstallDir() != filepath.Join(config.DataDir(), "bin") {
		t.Fatal("isolated install directory mismatch")
	}
}

func TestPathPreservesUnrelatedEntries(t *testing.T) {
	entry := `C:\Users\测试\Programs\qbmcp`
	for _, old := range []string{"", `C:\Windows;C:\工具`, `%SystemRoot%\System32;`, `C:\one;;C:\two`} {
		added := host.EditPath(old, entry, true)
		if host.EditPath(added, entry, true) != added {
			t.Fatal("PATH install is not idempotent")
		}
		if host.EditPath(added, entry, false) != old {
			t.Fatalf("uninstall changed original PATH %q", old)
		}
	}
	if got := host.EditPath(`C:\before;"C:\Users\测试\Programs\qbmcp\";C:\after`, entry, false); got != `C:\before;C:\after` {
		t.Fatal(got)
	}
}

func TestManagementMutexSerializesCommands(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	release, err := host.Lock("test", 0)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		other, e := host.Lock("test", 30)
		if e == nil {
			other()
		}
		result <- e
	}()
	if err = <-result; !errors.Is(err, host.ErrAlreadyRunning) {
		t.Fatalf("concurrent lock: %v", err)
	}
	release()
	release, err = host.Lock("test", 30)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestProcessIdentityRejectsReusedPID(t *testing.T) {
	stamp, err := host.ProcessStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !host.RuntimeAlive(host.RuntimeStatus{PID: os.Getpid(), ProcessStart: stamp}) {
		t.Fatal("own process not alive")
	}
	if host.RuntimeAlive(host.RuntimeStatus{PID: os.Getpid(), ProcessStart: stamp + 1}) {
		t.Fatal("reused PID accepted")
	}
}
