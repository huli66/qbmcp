//go:build windows

package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"qbmcp/internal/config"
)

//go:embed scheduler.ps1
var schedulerScript string

type TaskStatus struct {
	WatchEnabled   bool   `json:"watch_enabled"`
	WatchInstalled bool   `json:"watch_installed"`
	Installed      bool   `json:"installed"`
	Name           string `json:"name"`
	State          int    `json:"state"`
	Autostart      bool   `json:"autostart"`
	LastResult     int64  `json:"last_result"`
	Executable     string `json:"executable,omitempty"`
}

func currentSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func identity() (string, error) {
	sid, err := currentSID()
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(config.DataDir())
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(path))))
	return sid + "-" + hex.EncodeToString(hash[:6]), nil
}

func taskName() (string, error) {
	id, err := identity()
	return "qbmcp-" + id, err
}

func powershellPath() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

func encodedPS(script string) string {
	units := utf16.Encode([]rune(script))
	data := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2*i:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func backgroundArguments(command string) string {
	return command + " --data-dir " + syscall.EscapeArg(config.DataDir())
}

func scheduler(action string, enabled *bool) (TaskStatus, error) {
	var status TaskStatus
	name, err := taskName()
	if err != nil {
		return status, err
	}
	sid, err := currentSID()
	if err != nil {
		return status, err
	}
	input, err := json.Marshal(map[string]any{"action": action, "name": name, "sid": sid, "home": config.DataDir(), "executable": BackgroundPath(), "runner": backgroundArguments("_run"), "logonRunner": backgroundArguments("_autostart"), "watchRunner": backgroundArguments("_watch"), "enabled": enabled})
	if err != nil {
		return status, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, powershellPath(), "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", encodedPS(schedulerScript))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		return status, fmt.Errorf("用户计划任务 %s 失败: %s (%w)", action, strings.TrimSpace(stderr.String()), err)
	}
	if err = json.Unmarshal(bytes.TrimPrefix(stdout.Bytes(), []byte{0xef, 0xbb, 0xbf}), &status); err != nil {
		return status, fmt.Errorf("计划任务响应无效: %w", err)
	}
	return status, nil
}
