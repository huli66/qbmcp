# qbmcp

用户级 Windows 网页 MCP 桥接，当前版本 **0.3.1**。多个本地 MCP 客户端共享一个网页提供的工具。服务无需 token，所有工具由网页定义并执行。

## 快速入口

- [源码目录和实现流程](docs/architecture.md)
- [网页接入、消息协议和配置](docs/protocol.md)
- [详细打包、手动覆盖安装、验证和回退](docs/install.md)
- [测试说明](docs/testing.md)

## 项目目录

```text
cmd/qbmcp/         程序入口
internal/cli/      命令解析
internal/config/   配置与路径
internal/server/   MCP 与 HTTP
internal/bridge/   网页协议与调用队列
internal/host/     Windows 安装、后台、恢复和状态
internal/fileutil/ 文件写入
tests/            全部测试
scripts/          构建打包与安装
docs/             阅读与操作文档
dist/             构建产物
```

## 从源码构建

需要 Windows 10/11、Windows PowerShell 5.1、任务计划程序以及 Go 1.24.3 或更高版本。运行已打包程序不需要 Go 或 Node.js。

在仓库根目录：

```powershell
go test ./... -count=1 -timeout=60s
if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Static checks failed' }
.\scripts\build.ps1
```

输出 dist/qbmcp.exe、dist/qbmcp_0.3.1_windows_amd64.zip 和对应 SHA256 文件。脚本只编译打包，不安装。可用 -Architecture arm64 交叉构建 Windows ARM64 版本。

## 安装与更新

首次从源码构建后，使用普通 PowerShell：

```powershell
.\scripts\install.ps1 -Enable -Start
qbmcp status
```

发布压缩包解压后，直接使用包根目录的 install.ps1：

```powershell
.\install.ps1 -Enable -Start
```

已有安装请按 [覆盖安装流程](docs/install.md) 先备份、停止，再安装新版和启动。更新不需要 uninstall；安装器保留端口、Origin 和登录启动设置，保存配置时会移除废弃字段。

## 命令

| 命令 | 行为 |
|---|---|
| install | 安装或更新程序、计划任务及用户 PATH |
| start [--port N] | 启动后台；首次自动准备环境 |
| stop | 主动停止，不触发当前开机周期内的恢复 |
| enable [--port N] | 开启登录启动，不立即启动 |
| disable | 关闭登录启动，不停止当前进程 |
| status [--json] | 查询进程、端口、路径、网页和工具状态 |
| uninstall | 停止并删除本用户任务及 PATH 条目；保留文件 |
| help | 显示帮助 |

安装程序位于 %LOCALAPPDATA%\Programs\qbmcp，配置及日志位于 %LOCALAPPDATA%\qbmcp。设置 QBMCP_HOME 可运行独立实例，程序放在该目录的 bin；管理同一实例时必须使用相同的 QBMCP_HOME。

## 网页接入

默认 MCP 地址为 http://127.0.0.1:32300/mcp，WebSocket 地址为 ws://127.0.0.1:32300/ws。自己的网页需通过本地 HTTP 服务访问，并将确切 Origin 加入配置的 allowed_origins。

网页主动注册工具后，MCP 直接暴露这些工具；更新和断开会通知客户端刷新列表。尚未注册时列表为空。详细消息格式见 [协议文档](docs/protocol.md)。
