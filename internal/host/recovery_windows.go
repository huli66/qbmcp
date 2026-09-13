//go:build windows

package host

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"

	"qbmcp/internal/config"
	"qbmcp/internal/fileutil"
)

// Task Scheduler recovery may already be queued when stop runs.
// Bind a stop marker to this boot rather than disabling next-boot logon startup.
func CurrentBootID() (string, error) {
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

func SetStopMarker(stopped bool) error {
	path := filepath.Join(config.DataDir(), "manual-stop")
	if !stopped {
		return fileutil.RemoveIfExists(path)
	}
	id, err := CurrentBootID()
	if err != nil {
		return err
	}
	return fileutil.Write(path, []byte(id))
}

func StoppedThisBoot() (bool, error) {
	marker, err := os.ReadFile(filepath.Join(config.DataDir(), "manual-stop"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	id, err := CurrentBootID()
	return string(marker) == id, err
}

func SetBootFlag(name string, enabled bool) error {
	path := filepath.Join(config.DataDir(), name)
	if !enabled {
		return fileutil.RemoveIfExists(path)
	}
	id, err := CurrentBootID()
	if err != nil {
		return err
	}
	return fileutil.WriteJSON(path, id)
}

func BootFlag(name string) bool {
	b, err := os.ReadFile(filepath.Join(config.DataDir(), name))
	if err != nil {
		return false
	}
	var saved string
	if json.Unmarshal(b, &saved) != nil {
		return false
	}
	current, err := CurrentBootID()
	return err == nil && saved == current
}

func RunWatch(autostart bool) error {
	unlock, err := Lock("management", 15000)
	if err != nil {
		return err
	}
	defer unlock()
	if stopped, err := StoppedThisBoot(); err != nil || stopped {
		return err
	}
	if autostart {
		if err = SetBootFlag("desired-boot", true); err != nil {
			return err
		}
	}
	if !BootFlag("desired-boot") || BootFlag("failed-boot") {
		return nil
	}
	if state, _ := ReadRuntime(); RuntimeAlive(state) {
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
