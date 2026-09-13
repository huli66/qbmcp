//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type UserStatus struct {
	Version         string     `json:"version"`
	Mode            string     `json:"mode"`
	State           string     `json:"state"`
	Autostart       bool       `json:"autostart"`
	Task            TaskStatus `json:"task"`
	PID             int        `json:"pid"`
	Port            int        `json:"port"`
	PortSource      string     `json:"port_source"`
	Address         string     `json:"address"`
	MCPURL          string     `json:"mcp_url"`
	WebSocketURL    string     `json:"websocket_url"`
	HealthURL       string     `json:"health_url"`
	DemoURL         string     `json:"demo_url"`
	Executable      string     `json:"executable"`
	Background      string     `json:"background_executable"`
	InstallDir      string     `json:"install_dir"`
	DataDir         string     `json:"data_dir"`
	ConfigPath      string     `json:"config_path"`
	LogPath         string     `json:"log_path"`
	RuntimePath     string     `json:"runtime_path"`
	ConfigExists    bool       `json:"config_exists"`
	TokenConfigured bool       `json:"token_configured"`
	Health          *Health    `json:"health,omitempty"`
	Error           string     `json:"error,omitempty"`
}

// Paths and the configured port remain available even if the process is
// stopped or Task Scheduler is inaccessible. Never include the config token.
func statusDetails() UserStatus {
	s := UserStatus{
		Version: version, Mode: "user", State: "not_installed", DataDir: dataDir(),
		InstallDir: installDir(), Executable: filepath.Join(installDir(), "qbmcp.exe"),
		Background: backgroundPath(), ConfigPath: configPath(),
		LogPath: filepath.Join(dataDir(), "logs", "qbmcp.log"), RuntimePath: filepath.Join(dataDir(), "runtime.json"),
	}
	c, err := loadConfig()
	if errors.Is(err, os.ErrNotExist) {
		s.setPort(32300, "default")
	} else if err != nil {
		s.ConfigExists = true
		s.PortSource = "unavailable"
		s.Error = "配置不可用: " + err.Error()
	} else {
		s.ConfigExists, s.TokenConfigured = true, true
		s.setPort(c.Port, "config")
	}
	return s
}

func (s *UserStatus) setPort(port int, source string) {
	s.Port, s.PortSource = port, source
	s.Address = fmt.Sprintf("127.0.0.1:%d", port)
	s.MCPURL, s.HealthURL, s.DemoURL = "http://"+s.Address+"/mcp", "http://"+s.Address+"/health", "http://"+s.Address+"/demo"
	s.WebSocketURL = "ws://" + s.Address + "/ws"
}

func (s *UserStatus) addError(err error) {
	if s.Error != "" {
		s.Error += "; "
	}
	s.Error += err.Error()
}

func queryUserStatus() (UserStatus, error) {
	result := statusDetails()
	task, taskErr := scheduler("query", nil)
	result.Task, result.Autostart = task, task.Autostart
	if taskErr != nil {
		result.State = "unknown"
		result.addError(taskErr)
	}
	state, readErr := readRuntime()
	if runtimeAlive(state) {
		result.State, result.PID = state.State, state.PID
		// A config edit takes effect after restart. Report the actual live port.
		if state.Port > 0 {
			result.setPort(state.Port, "runtime")
			var err error
			result.Health, err = getHealth(Config{Port: state.Port}, state.PID)
			if err != nil {
				result.addError(fmt.Errorf("后台进程存在，但健康接口不可用: %w", err))
			}
		}
	} else if task.Installed {
		result.State = "stopped"
		if task.State == 4 || task.State == 2 {
			result.State = "starting"
		}
		if readErr == nil && state.State == "failed" {
			result.State = "failed"
			result.addError(errors.New(state.Error))
		}
		if readErr == nil && state.State == "running" {
			result.State = "recovering"
		}
		if stopped, _ := stoppedThisBoot(); stopped {
			result.State = "stopped"
		}
		if !bootFlag("desired-boot") {
			result.State = "stopped"
		}
	}
	return result, taskErr
}

func writeUserStatus(w io.Writer, s UserStatus, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	fmt.Fprintf(w, "版本: %s\n模式: 当前用户\n状态: %s\n登录启动: %t\nPID: %d\n", s.Version, s.State, s.Autostart, s.PID)
	fmt.Fprintf(w, "主任务: %s\n任务状态: %d（2=排队，3=就绪，4=运行）\n恢复检查: 已安装=%t，已启用=%t\n", s.Task.Name, s.Task.State, s.Task.WatchInstalled, s.Task.WatchEnabled)
	if s.Port != 0 {
		fmt.Fprintf(w, "端口: %d（来源: %s）\n监听地址: %s\nMCP: %s\nWebSocket: %s\n健康检查: %s\n测试页面: %s\n", s.Port, s.PortSource, s.Address, s.MCPURL, s.WebSocketURL, s.HealthURL, s.DemoURL)
	} else {
		fmt.Fprintln(w, "端口: 未知（配置不可用）")
	}
	fmt.Fprintf(w, "命令程序: %s\n后台程序: %s\n安装目录: %s\n数据目录: %s\n配置文件: %s\n日志文件: %s\n运行记录: %s\n凭据已配置: %t\n", s.Executable, s.Background, s.InstallDir, s.DataDir, s.ConfigPath, s.LogPath, s.RuntimePath, s.TokenConfigured)
	if s.Health != nil {
		h := s.Health
		fmt.Fprintf(w, "运行版本: %s\n运行时长: %s\n网页已连接: %t\n工具就绪: %t\n网页工具: %d（另有 2 个内置工具）\nMCP 会话: %d\n待处理请求: %d\n", h.Version, time.Duration(h.UptimeSeconds)*time.Second, h.PageConnected, h.Ready, h.ToolCount, h.MCPSessions, h.PendingRequests)
	} else {
		fmt.Fprintln(w, "网页/工具/MCP 会话: 未查询到健康状态")
	}
	if s.Error != "" {
		fmt.Fprintln(w, "错误:", s.Error)
	}
	return nil
}

func printUserStatus(asJSON bool) error {
	s, err := queryUserStatus()
	if writeErr := writeUserStatus(os.Stdout, s, asJSON); writeErr != nil {
		return writeErr
	}
	return err
}
