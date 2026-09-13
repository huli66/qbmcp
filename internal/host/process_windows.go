//go:build windows

package host

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"

	"qbmcp/internal/config"
	"qbmcp/internal/fileutil"
)

type RuntimeStatus struct {
	ConsoleAttached bool      `json:"console_attached"`
	State           string    `json:"state"`
	PID             int       `json:"pid"`
	ProcessStart    uint64    `json:"process_start"`
	Port            int       `json:"port"`
	Error           string    `json:"error,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func ReadRuntime() (RuntimeStatus, error) {
	var state RuntimeStatus
	b, err := os.ReadFile(filepath.Join(config.DataDir(), "runtime.json"))
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(b, &state)
	return state, err
}

func writeRuntime(state RuntimeStatus) error {
	state.UpdatedAt = time.Now().UTC()
	return fileutil.WriteJSON(filepath.Join(config.DataDir(), "runtime.json"), state)
}

func ProcessStart(pid int) (uint64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)
	state, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return 0, err
	}
	if state != uint32(windows.WAIT_TIMEOUT) {
		return 0, errors.New("process exited")
	}
	var start, end, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &start, &end, &kernel, &user); err != nil {
		return 0, err
	}
	return uint64(start.HighDateTime)<<32 | uint64(start.LowDateTime), nil
}

func RuntimeAlive(state RuntimeStatus) bool {
	if state.PID <= 0 || state.ProcessStart == 0 {
		return false
	}
	start, err := ProcessStart(state.PID)
	return err == nil && start == state.ProcessStart
}
