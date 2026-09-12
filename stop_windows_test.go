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
	t.Setenv("ProgramData", t.TempDir())
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
