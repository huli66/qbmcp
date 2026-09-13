//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootIDStable(t *testing.T) {
	a, err := currentBootID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := currentBootID()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 || a != b {
		t.Fatalf("unexpected boot IDs %q, %q", a, b)
	}
}

func TestStopMarkerLifecycle(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	if err := os.MkdirAll(dataDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := setStopMarker(true); err != nil {
		t.Fatal(err)
	}
	if stopped, err := stoppedThisBoot(); err != nil || !stopped {
		t.Fatalf("stop marker: %v %v", stopped, err)
	}
	// A marker from a different boot must not suppress autostart.
	if err := os.WriteFile(filepath.Join(dataDir(), "manual-stop"), []byte(strings.Repeat("0", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	if stopped, err := stoppedThisBoot(); err != nil || stopped {
		t.Fatalf("previous boot: %v %v", stopped, err)
	}
	if err := setStopMarker(false); err != nil {
		t.Fatal(err)
	}
	if stopped, err := stoppedThisBoot(); err != nil || stopped {
		t.Fatalf("clear marker: %v %v", stopped, err)
	}
}

func TestWatchHonorsIntentStopAndStartupFailure(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	if err := os.MkdirAll(dataDir(), 0700); err != nil {
		t.Fatal(err)
	}
	// All paths return before touching Task Scheduler when no recovery is due.
	if err := runWatch(false); err != nil {
		t.Fatal(err)
	}
	if err := setStopMarker(true); err != nil {
		t.Fatal(err)
	}
	if err := runWatch(true); err != nil {
		t.Fatal(err)
	}
	if bootFlag("desired-boot") {
		t.Fatal("login bypassed manual stop")
	}
	if err := setStopMarker(false); err != nil {
		t.Fatal(err)
	}
	if err := setBootFlag("desired-boot", true); err != nil {
		t.Fatal(err)
	}
	if err := setBootFlag("failed-boot", true); err != nil {
		t.Fatal(err)
	}
	if err := runWatch(false); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(dataDir(), "desired-boot"), strings.Repeat("0", 32)); err != nil {
		t.Fatal(err)
	}
	if bootFlag("desired-boot") {
		t.Fatal("previous boot intent accepted")
	}
}
