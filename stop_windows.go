//go:build windows

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SCM recovery may already be queued when stop observes SERVICE_STOPPED.
// Bind a stop marker to this boot rather than disabling next-boot autostart.
func currentBootID() (string, error) {
	var info struct {
		Identifier   [16]byte
		FirmwareType uint32
		BootFlags    uint64
	}
	err := windows.NtQuerySystemInformation(windows.SystemBootEnvironmentInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil)
	if err != nil {
		return "", fmt.Errorf("读取 Windows 启动标识失败: %w", err)
	}
	return hex.EncodeToString(info.Identifier[:]), nil
}
func setStopMarker(stopped bool) error {
	path := filepath.Join(dataDir(), "manual-stop")
	if !stopped {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	id, err := currentBootID()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dataDir(), "stop-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(id); err != nil {
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
func stoppedThisBoot() (bool, error) {
	marker, err := os.ReadFile(filepath.Join(dataDir(), "manual-stop"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	id, err := currentBootID()
	return string(marker) == id, err
}
