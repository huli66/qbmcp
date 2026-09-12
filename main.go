package main

import (
	"flag"
	"fmt"
	"os"
)

const usage = `qbmcp - Windows 网页 MCP 桥接服务

用法:
  qbmcp start [--port 32300]  注册（若需要）并启动服务
  qbmcp stop                停止服务，不触发异常恢复
  qbmcp enable [--port N]    设置开机启动，不立即启动
  qbmcp disable             关闭开机启动，不停止当前服务
  qbmcp status [--json]      查看 Windows 服务及网页连接状态
  qbmcp help                显示帮助

服务管理需要管理员终端。配置/token: %ProgramData%\qbmcp\config.json
日志: %ProgramData%\qbmcp\logs\qbmcp.log
默认测试页: http://127.0.0.1:32300/demo
默认 MCP: http://127.0.0.1:32300/mcp
端口修改前必须先 stop。stop 保留下次开机启动设置。
首次注册会复制可执行文件到 %ProgramData%\qbmcp\bin。
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
	if command == "service" {
		return runService()
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	port := 0
	asJSON := false
	switch command {
	case "start", "enable":
		f.IntVar(&port, "port", 0, "监听端口")
	case "status":
		f.BoolVar(&asJSON, "json", false, "JSON 输出")
	case "stop", "disable":
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
	return manageService(command, port, asJSON)
}
