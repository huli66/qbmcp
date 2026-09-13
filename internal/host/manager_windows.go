//go:build windows

package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"qbmcp/internal/config"
)

func stopUser() error {
	if _, err := os.Stat(config.DataDir()); !errors.Is(err, os.ErrNotExist) {
		if err = SetStopMarker(true); err != nil {
			return err
		}
		if err = SetBootFlag("desired-boot", false); err != nil {
			return err
		}
		if err = signalStop(); err != nil {
			return err
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			state, _ := ReadRuntime()
			if !RuntimeAlive(state) {
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
		state, _ := ReadRuntime()
		if !RuntimeAlive(state) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("后台程序仍在停止，请查看 status 和日志")
}

func Manage(command string, port int, asJSON bool) error {
	if command == "status" {
		return printUserStatus(asJSON)
	}
	unlock, err := Lock("management", 30000)
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
	state, _ := ReadRuntime()
	active := RuntimeAlive(state) || task.State == 4 || task.State == 2
	if active && port != 0 {
		c, e := config.Load()
		if e != nil {
			return e
		}
		if c.Port != port {
			return fmt.Errorf("运行中不能修改端口，请先 qbmcp stop")
		}
	}
	if command == "start" && active {
		if state.State == "running" && RuntimeAlive(state) {
			fmt.Println("后台程序已运行")
			return nil
		}
		return waitUserReady()
	}
	if _, err = PrepareConfig(port); err != nil {
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
		fmt.Println("已安装到", config.InstallDir())
		fmt.Println("用户 PATH 已更新，请重新打开终端后使用 qbmcp help")
	case "enable", "disable":
		fmt.Println("登录启动设置已更新；当前运行状态未改变")
	case "start":
		if err = SetStopMarker(false); err != nil {
			return err
		}
		if err = SetBootFlag("failed-boot", false); err != nil {
			return err
		}
		if err = SetBootFlag("desired-boot", true); err != nil {
			return err
		}
		if err = os.Remove(filepath.Join(config.DataDir(), "runtime.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err = scheduler("run", nil); err != nil {
			return err
		}
		if err = waitUserReady(); err != nil {
			return err
		}
		fmt.Println("后台程序已启动；配置文件位于", config.Path())
	}
	return nil
}
