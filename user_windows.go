//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func protectUserDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("运行目录不能是符号链接或目录联接: %s", path)
	}
	sid, err := currentSID()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
func prepareConfig(port int) (Config, error) {
	if err := protectUserDir(dataDir()); err != nil {
		return Config{}, err
	}
	if err := protectUserDir(filepath.Join(dataDir(), "logs")); err != nil {
		return Config{}, err
	}
	c, err := loadConfig()
	if errors.Is(err, os.ErrNotExist) {
		c = Config{Port: 32300, Token: randomID(), AllowedOrigins: []string{}}
	} else if err != nil {
		return c, err
	}
	if port != 0 {
		c.Port = port
	}
	return c, saveConfig(c)
}
func managedExecutable() error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	dir := installDir()
	if err = protectUserDir(dir); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	background, err := backgroundImage(data)
	if err != nil {
		return err
	}
	if err = installImage(filepath.Join(dir, "qbmcp.exe"), data); err != nil {
		return err
	}
	return installImage(backgroundPath(), background)
}

// Preserve unrelated PATH entries and expandable registry values.
func editPath(old, entry string, add bool) string {
	var parts []string
	for _, part := range strings.Split(old, ";") {
		if strings.EqualFold(filepath.Clean(strings.Trim(strings.TrimSpace(part), `"`)), filepath.Clean(entry)) {
			continue
		}
		parts = append(parts, part)
	}
	if add {
		if len(parts) == 1 && parts[0] == "" {
			parts = nil
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, ";")
}
func updateUserPath(add bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	old, kind, err := key.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	value := editPath(old, installDir(), add)
	if value == old {
		return nil
	}
	if kind == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", value)
	} else {
		err = key.SetStringValue("Path", value)
	}
	if err != nil {
		return err
	}
	environment, _ := windows.UTF16PtrFromString("Environment")
	var ignored uintptr
	windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(environment)), 2, 2000, uintptr(unsafe.Pointer(&ignored)))
	return nil
}
func stopUser() error {
	if _, err := os.Stat(dataDir()); !errors.Is(err, os.ErrNotExist) {
		if err = setStopMarker(true); err != nil {
			return err
		}
		if err = setBootFlag("desired-boot", false); err != nil {
			return err
		}
		if err = signalStop(); err != nil {
			return err
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			state, _ := readRuntime()
			if !runtimeAlive(state) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if _, err := scheduler("stop", nil); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := readRuntime()
		if !runtimeAlive(state) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("后台程序仍在停止，请查看 status 和日志")
}
func manageUser(command string, port int, asJSON bool) error {
	if command == "status" {
		return printUserStatus(asJSON)
	}
	unlock, err := lockNamed("management", 30000)
	if err != nil {
		return err
	}
	defer unlock()
	if command == "stop" || command == "uninstall" {
		if err = stopUser(); err != nil {
			return err
		}
		if command == "uninstall" {
			if _, err = scheduler("delete", nil); err != nil {
				return err
			}
			if err = updateUserPath(false); err != nil {
				return err
			}
			fmt.Println("已移除用户计划任务和 PATH；程序、配置及日志文件已保留")
		} else {
			fmt.Println("后台程序已停止；登录启动设置未改变")
		}
		return nil
	}
	task, err := scheduler("query", nil)
	if err != nil {
		return err
	}
	if command == "disable" && !task.Installed {
		fmt.Println("未安装用户计划任务，无需操作")
		return nil
	}
	state, _ := readRuntime()
	active := runtimeAlive(state) || task.State == 4 || task.State == 2
	if active && port != 0 {
		c, e := loadConfig()
		if e != nil {
			return e
		}
		if c.Port != port {
			return fmt.Errorf("运行中不能修改端口，请先 qbmcp stop")
		}
	}
	if command == "start" && active {
		if state.State == "running" && runtimeAlive(state) {
			fmt.Println("后台程序已运行")
			return nil
		}
		return waitUserReady()
	}
	if _, err = prepareConfig(port); err != nil {
		return err
	}
	// Quiesce the short-lived watchdog during a stopped install/update so it
	// cannot hold the background image open while waiting for our mutex.
	if !active && task.WatchInstalled {
		if _, err = scheduler("pause_watch", nil); err != nil {
			return err
		}
		defer func() {
			if task.WatchEnabled {
				if _, e := scheduler("resume_watch", nil); e != nil {
					fmt.Fprintln(os.Stderr, "恢复检查任务失败，请重新执行 install:", e)
				}
			}
		}()
	}
	if err = managedExecutable(); err != nil {
		return err
	}
	var enabled *bool
	if command == "enable" || command == "disable" {
		v := command == "enable"
		enabled = &v
	}
	if _, err = scheduler("ensure", enabled); err != nil {
		return err
	}
	switch command {
	case "install":
		if err = updateUserPath(true); err != nil {
			return err
		}
		fmt.Println("已安装到", installDir())
		fmt.Println("用户 PATH 已更新，请重新打开终端后使用 qbmcp help")
	case "enable", "disable":
		fmt.Println("登录启动设置已更新；当前运行状态未改变")
	case "start":
		if err = setStopMarker(false); err != nil {
			return err
		}
		if err = setBootFlag("failed-boot", false); err != nil {
			return err
		}
		if err = setBootFlag("desired-boot", true); err != nil {
			return err
		}
		if err = os.Remove(filepath.Join(dataDir(), "runtime.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err = scheduler("run", nil); err != nil {
			return err
		}
		if err = waitUserReady(); err != nil {
			return err
		}
		fmt.Println("后台程序已启动；token 位于", configPath())
	}
	return nil
}
func getHealth(c Config, pid int) (*Health, error) {
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", c.Port))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var health Health
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("/health 返回 HTTP %d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&health); err != nil {
		return nil, err
	}
	if health.Service != "running" || health.PID != pid {
		return nil, fmt.Errorf("端口上的进程不是当前用户的 qbmcp 实例")
	}
	return &health, nil
}
func waitUserReady() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := readRuntime()
		if state.State == "failed" {
			return fmt.Errorf("启动失败: %s", state.Error)
		}
		if runtimeAlive(state) {
			if _, err = getHealth(c, state.PID); err == nil {
				return nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("启动超时，请用 qbmcp status 检查用户计划任务和日志")
}
