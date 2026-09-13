package main

import (
	"flag"
	"fmt"
	"os"
)

const usage = `qbmcp - 用户级 Windows 网页 MCP 桥接

用法:
  qbmcp install            安装到当前用户目录并添加用户 PATH
  qbmcp start [--port N]    启动后台程序；首次自动准备运行环境
  qbmcp stop                正常停止，不触发异常恢复
  qbmcp enable [--port N]   开启当前用户登录启动，不立即启动
  qbmcp disable             关闭登录启动，不停止后台程序
  qbmcp status [--json]     查看端口、路径、后台程序及网页连接状态
  qbmcp uninstall          停止并删除用户计划任务、移除 PATH，保留文件
  qbmcp help                显示帮助

以上命令使用普通终端，无需管理员权限。
程序: %LOCALAPPDATA%\Programs\qbmcp\qbmcp.exe
配置/token: %LOCALAPPDATA%\qbmcp\config.json
日志: %LOCALAPPDATA%\qbmcp\logs\qbmcp.log
默认测试页: http://127.0.0.1:32300/demo
默认 MCP: http://127.0.0.1:32300/mcp
端口修改前必须先 stop。stop 保留下次启动 Windows 后的登录启动设置。
异常退出由用户计划任务按 1 分钟间隔恢复；后台入口不创建控制台窗口。
`

func main() {
	if err := runCLI(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "qbmcp:", err)
		os.Exit(1)
	}
}
func runCLI(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	command := args[0]
	if command == "help" || command == "--help" || command == "-h" {
		fmt.Print(usage)
		return nil
	}
	if command == "_run" || command == "_watch" || command == "_autostart" {
		internal := flag.NewFlagSet("_run", flag.ContinueOnError)
		home := internal.String("data-dir", "", "运行目录")
		if err := internal.Parse(args[1:]); err != nil {
			return err
		}
		if *home == "" || internal.NArg() != 0 {
			return fmt.Errorf("内部入口需要 --data-dir")
		}
		if err := os.Setenv("QBMCP_HOME", *home); err != nil {
			return err
		}
		if command == "_run" {
			return runBackground()
		}
		return runWatch(command == "_autostart")
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	port := 0
	asJSON := false
	switch command {
	case "start", "enable":
		f.IntVar(&port, "port", 0, "监听端口")
	case "status":
		f.BoolVar(&asJSON, "json", false, "JSON 输出")
	case "stop", "disable", "install", "uninstall":
	default:
		return fmt.Errorf("未知命令 %q；使用 qbmcp help 查看帮助", command)
	}
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("不支持额外位置参数")
	}
	portSet := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "port" {
			portSet = true
		}
	})
	if portSet && (port < 1 || port > 65535) {
		return fmt.Errorf("端口必须在 1..65535 之间")
	}
	return manageUser(command, port, asJSON)
}
