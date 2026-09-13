//go:build windows

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qbmcp/internal/config"
	"qbmcp/internal/fileutil"
	"qbmcp/internal/host"
)

func TestBootIDStable(t *testing.T) {
	a, err := host.CurrentBootID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := host.CurrentBootID()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 || a != b {
		t.Fatalf("unexpected boot IDs %q, %q", a, b)
	}
}

func TestStopMarkerLifecycle(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	if err := os.MkdirAll(config.DataDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := host.SetStopMarker(true); err != nil {
		t.Fatal(err)
	}
	if stopped, err := host.StoppedThisBoot(); err != nil || !stopped {
		t.Fatalf("stop marker: %v %v", stopped, err)
	}
	// A marker from a different boot must not suppress autostart.
	if err := os.WriteFile(filepath.Join(config.DataDir(), "manual-stop"), []byte(strings.Repeat("0", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	if stopped, err := host.StoppedThisBoot(); err != nil || stopped {
		t.Fatalf("previous boot: %v %v", stopped, err)
	}
	if err := host.SetStopMarker(false); err != nil {
		t.Fatal(err)
	}
	if stopped, err := host.StoppedThisBoot(); err != nil || stopped {
		t.Fatalf("clear marker: %v %v", stopped, err)
	}
}

func TestWatchHonorsIntentStopAndStartupFailure(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	if err := os.MkdirAll(config.DataDir(), 0700); err != nil {
		t.Fatal(err)
	}
	// All paths return before touching Task Scheduler when no recovery is due.
	if err := host.RunWatch(false); err != nil {
		t.Fatal(err)
	}
	if err := host.SetStopMarker(true); err != nil {
		t.Fatal(err)
	}
	if err := host.RunWatch(true); err != nil {
		t.Fatal(err)
	}
	if host.BootFlag("desired-boot") {
		t.Fatal("login bypassed manual stop")
	}
	if err := host.SetStopMarker(false); err != nil {
		t.Fatal(err)
	}
	if err := host.SetBootFlag("desired-boot", true); err != nil {
		t.Fatal(err)
	}
	if err := host.SetBootFlag("failed-boot", true); err != nil {
		t.Fatal(err)
	}
	if err := host.RunWatch(false); err != nil {
		t.Fatal(err)
	}
	if err := fileutil.WriteJSON(filepath.Join(config.DataDir(), "desired-boot"), strings.Repeat("0", 32)); err != nil {
		t.Fatal(err)
	}
	if host.BootFlag("desired-boot") {
		t.Fatal("previous boot intent accepted")
	}
}
