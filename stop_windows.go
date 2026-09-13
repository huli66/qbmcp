//go:build windows

package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Task Scheduler recovery may already be queued when stop runs.
// Bind a stop marker to this boot rather than disabling next-boot logon startup.
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

func setBootFlag(name string, enabled bool) error {
	path := filepath.Join(dataDir(), name)
	if !enabled {
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
	return atomicJSON(path, id)
}
func bootFlag(name string) bool {
	b, err := os.ReadFile(filepath.Join(dataDir(), name))
	if err != nil {
		return false
	}
	var saved string
	if json.Unmarshal(b, &saved) != nil {
		return false
	}
	current, err := currentBootID()
	return err == nil && saved == current
}
func runWatch(autostart bool) error {
	unlock, err := lockNamed("management", 15000)
	if err != nil {
		return err
	}
	defer unlock()
	if stopped, err := stoppedThisBoot(); err != nil || stopped {
		return err
	}
	if autostart {
		if err = setBootFlag("desired-boot", true); err != nil {
			return err
		}
	}
	if !bootFlag("desired-boot") || bootFlag("failed-boot") {
		return nil
	}
	if state, _ := readRuntime(); runtimeAlive(state) {
		return nil
	}
	task, err := scheduler("query", nil)
	if err != nil {
		return err
	}
	if !task.Installed || task.State == 4 || task.State == 2 {
		return nil
	}
	_, err = scheduler("run", nil)
	return err
}
