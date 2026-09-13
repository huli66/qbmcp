//go:build windows

package main

import (
	"bytes"
	"debug/pe"
	"os"
	"testing"
)

func TestBackgroundImage(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	copyBefore := bytes.Clone(original)
	background, err := backgroundImage(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copyBefore, original) {
		t.Fatal("modified CLI image")
	}
	f, err := pe.NewFile(bytes.NewReader(background))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		if h.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
			t.Fatal("console subsystem")
		}
	case *pe.OptionalHeader64:
		if h.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
			t.Fatal("console subsystem")
		}
	default:
		t.Fatal("missing header")
	}
	// Installation is idempotent and malformed inputs fail without panicking.
	again, err := backgroundImage(background)
	if err != nil || !bytes.Equal(again, background) {
		t.Fatal("not idempotent", err)
	}
	for _, bad := range [][]byte{nil, []byte("invalid"), original[:64], original[:128]} {
		if _, err := backgroundImage(bad); err == nil {
			t.Fatal("accepted truncated image")
		}
	}
}
