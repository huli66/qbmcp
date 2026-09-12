//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"gopkg.in/natefinch/lumberjack.v2"
)

const serviceName = "qbmcp"

func protectDir(path, permissions string) error {
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
		return fmt.Errorf("服务目录不能是符号链接或目录联接: %s", path)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;" + permissions + ";;;LS)")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	owner, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, acl, nil)
}
func prepareConfig(port int) (Config, error) {
	if err := protectDir(dataDir(), "GRGX"); err != nil {
		return Config{}, err
	}
	if err := protectDir(filepath.Join(dataDir(), "logs"), "0x1301bf"); err != nil {
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
func managedExecutable() (string, error) {
	src, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir(), "bin")
	if err = protectDir(dir, "GRGX"); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "qbmcp.exe")
	if strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		return dst, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, "qbmcp-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	if err = out.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}
func managementLock() (func(), error) {
	n, _ := windows.UTF16PtrFromString(`Global\qbmcp-management`)
	h, err := windows.CreateMutex(nil, false, n)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	status, err := windows.WaitForSingleObject(h, 30000)
	if err != nil || (status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED) {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("另一个服务管理命令尚未完成")
	}
	return func() { windows.ReleaseMutex(h); windows.CloseHandle(h) }, nil
}
func manageService(command string, port int, asJSON bool) error {
	if command == "status" {
		return printStatus(asJSON)
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("请在管理员 PowerShell/终端中运行此命令")
	}
	unlock, err := managementLock()
	if err != nil {
		return err
	}
	defer unlock()
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return err
	}
	if s == nil && (command == "stop" || command == "disable") {
		fmt.Println("服务未注册，无需操作")
		return nil
	}
	if s != nil {
		defer func() { s.Close() }()
	}
	var state svc.State = svc.Stopped
	if s != nil {
		st, e := s.Query()
		if e != nil {
			return e
		}
		state = st.State
	}
	if command == "start" || command == "enable" {
		if state != svc.Stopped && port != 0 {
			c, e := loadConfig()
			if e != nil {
				return e
			}
			if c.Port != port {
				return fmt.Errorf("服务运行中不能修改端口，请先 qbmcp stop")
			}
		}
		if _, err = prepareConfig(port); err != nil {
			return err
		}
		if s == nil {
			exe, e := managedExecutable()
			if e != nil {
				return e
			}
			s, err = m.CreateService(serviceName, exe, mgr.Config{DisplayName: "qbmcp Webpage MCP Bridge", Description: "Local MCP and webpage WebSocket bridge", StartType: mgr.StartManual, ServiceStartName: `NT AUTHORITY\LocalService`}, "service")
			if err != nil {
				return err
			}
			defer s.Close()
		} else if state == svc.Stopped {
			// Update only while stopped; the SCM always uses the protected copy.
			if _, err = managedExecutable(); err != nil {
				return err
			}
		}
		if err = s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}, {Type: mgr.ServiceRestart, Delay: 15 * time.Second}, {Type: mgr.ServiceRestart, Delay: 60 * time.Second}}, 86400); err != nil {
			return err
		}
		if err = s.SetRecoveryActionsOnNonCrashFailures(false); err != nil {
			return err
		}
	}
	switch command {
	case "enable", "disable":
		c, e := s.Config()
		if e != nil {
			return e
		}
		c.StartType = mgr.StartManual
		if command == "enable" {
			c.StartType = mgr.StartAutomatic
		}
		if err = s.UpdateConfig(c); err != nil {
			return err
		}
		fmt.Println("开机启动设置已更新；当前运行状态未改变")
	case "start":
		if err = setStopMarker(false); err != nil {
			return err
		}
		if state == svc.Running {
			fmt.Println("服务已运行")
			return nil
		}
		if state == svc.StartPending {
			return waitState(s, svc.Running)
		}
		if state != svc.Stopped {
			return fmt.Errorf("服务正在转换状态，请稍后重试")
		}
		if err = s.Start(); err != nil {
			return err
		}
		if err = waitState(s, svc.Running); err != nil {
			return err
		}
		fmt.Println("服务已启动；token 位于", configPath())
	case "stop":
		if err = setStopMarker(true); err != nil {
			return err
		}
		// A queued recovery may have begun since the first status query.
		deadline := time.Now().Add(30 * time.Second)
		for {
			latest, e := s.Query()
			if e != nil {
				return e
			}
			state = latest.State
			if state != svc.StartPending {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("服务仍在启动，停止标记已保存；请稍后再次 stop")
			}
			time.Sleep(200 * time.Millisecond)
		}
		if state == svc.Stopped {
			fmt.Println("服务已停止")
			return nil
		}
		if state != svc.StopPending {
			if _, err = s.Control(svc.Stop); err != nil {
				return err
			}
		}
		if err = waitState(s, svc.Stopped); err != nil {
			return err
		}
		fmt.Println("服务已停止；开机启动设置未改变")
	}
	return nil
}
func waitState(s *mgr.Service, want svc.State) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if want == svc.Running && st.State == svc.Stopped {
			return fmt.Errorf("服务启动失败（Windows 错误 %d）；请查看 %s", st.Win32ExitCode, filepath.Join(dataDir(), "logs", "qbmcp.log"))
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待服务状态超时，请使用 status 检查")
}

type ServiceStatus struct {
	State           string  `json:"state"`
	Autostart       bool    `json:"autostart"`
	PID             uint32  `json:"pid,omitempty"`
	WindowsExitCode uint32  `json:"windows_exit_code,omitempty"`
	Health          *Health `json:"health,omitempty"`
	HealthError     string  `json:"health_error,omitempty"`
}

func queryStatus() (ServiceStatus, error) {
	result := ServiceStatus{State: "not_installed"}
	// Query-only handles let status work without service-management privileges.
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return result, err
	}
	defer windows.CloseServiceHandle(h)
	n, _ := windows.UTF16PtrFromString(serviceName)
	sh, err := windows.OpenService(h, n, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	s := &mgr.Service{Name: serviceName, Handle: sh}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return result, err
	}
	c, err := s.Config()
	if err != nil {
		return result, err
	}
	result.State = map[svc.State]string{svc.Stopped: "stopped", svc.StartPending: "starting", svc.StopPending: "stopping", svc.Running: "running", svc.Paused: "paused"}[st.State]
	if result.State == "" {
		result.State = fmt.Sprintf("state_%d", st.State)
	}
	result.Autostart = c.StartType == mgr.StartAutomatic
	result.PID = st.ProcessId
	result.WindowsExitCode = st.Win32ExitCode
	if st.State != svc.Running {
		return result, nil
	}
	conf, err := loadConfig()
	if err != nil {
		result.HealthError = "无法读取配置，请用管理员终端查看完整连接状态"
		return result, nil
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", conf.Port))
	if err != nil {
		result.HealthError = "服务进程运行中，但 /health 不可达"
		return result, nil
	}
	defer resp.Body.Close()
	var health Health
	if resp.StatusCode != 200 {
		result.HealthError = fmt.Sprintf("/health 返回 HTTP %d", resp.StatusCode)
	} else if err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&health); err != nil || health.Service != "running" {
		result.HealthError = "/health 响应无效"
	} else {
		result.Health = &health
	}
	return result, nil
}
func printStatus(asJSON bool) error {
	st, err := queryStatus()
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	fmt.Printf("服务: %s\n开机启动: %t\nPID: %d\n", st.State, st.Autostart, st.PID)
	if st.Health != nil {
		h := st.Health
		fmt.Printf("地址: %s\n网页已连接: %t\n工具就绪: %t\n网页工具: %d\nMCP 会话: %d\n待处理请求: %d\n", h.Address, h.PageConnected, h.Ready, h.ToolCount, h.MCPSessions, h.PendingRequests)
	}
	if st.HealthError != "" {
		fmt.Println(st.HealthError)
	}
	return nil
}
func runService() error {
	ok, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("内部入口只能由 Windows 服务管理器调用；请使用 start")
	}
	return svc.Run(serviceName, &serviceHandler{})
}

type serviceHandler struct{}

func (*serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending, WaitHint: 15000}
	writer := &lumberjack.Logger{Filename: filepath.Join(dataDir(), "logs", "qbmcp.log"), MaxSize: 5, MaxBackups: 3, MaxAge: 14, Compress: true}
	defer writer.Close()
	logger := slog.New(slog.NewJSONHandler(writer, nil))
	stopped, err := stoppedThisBoot()
	if err != nil {
		logger.Error("read stop marker failed", "error", err)
		return false, 1
	}
	if stopped {
		logger.Info("manual stop suppresses queued recovery for this boot")
		return false, 0
	}
	c, err := loadConfig()
	if err != nil {
		logger.Error("startup configuration failed", "error", err)
		return false, 1
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.Port))
	if err != nil {
		logger.Error("listen failed", "error", err)
		return false, 1
	}
	a := newApp(c, logger)
	server := &http.Server{Handler: a.handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	current := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	changes <- current
	logger.Info("service started", "port", c.Port)
	for {
		select {
		case err := <-done:
			logger.Error("HTTP server exited unexpectedly", "error", err)
			// Do not report STOPPED: SCM must apply crash recovery.
			os.Exit(1)
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- current
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending, WaitHint: 15000}
				a.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				_ = server.Shutdown(ctx)
				cancel()
				_ = server.Close()
				logger.Info("service stopped normally")
				return false, 0
			}
		}
	}
}
