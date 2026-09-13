# 打包与手动覆盖安装

本次版本为 **0.3.1**，编译入口改为 ./cmd/qbmcp。程序安装目录、数据目录、任务名称、端口和网页协议保持原有规则；无需 token，工具全部由网页提供。

以下使用普通 PowerShell，无需管理员权限。按顺序执行，每步成功后再继续；变量在同一个 PowerShell 窗口中使用。

## 1. 检查并打包

```powershell
Set-Location 'E:\Code\qbmcp'

go test ./... -count=1 -timeout=60s
if ($LASTEXITCODE -ne 0) { throw 'Tests failed; stop here.' }

go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Static checks failed; stop here.' }

.\scripts\build.ps1
```

build.ps1 默认生成 Windows amd64 版本，从 internal/config/config.go 读取版本号，嵌入任务管理脚本并打包。它不安装、不停止后台，也不修改已安装配置。

| 产物 | 用途 |
|---|---|
| dist/qbmcp.exe | 可直接安装的命令行程序 |
| dist/qbmcp_0.3.1_windows_amd64.zip | 可分发给同架构 Windows 电脑的完整发布包 |
| dist/qbmcp_0.3.1_windows_amd64.zip.sha256 | 压缩包 SHA256 校验值 |

Windows ARM64 构建使用 .\scripts\build.ps1 -Architecture arm64。选择实际运行电脑的架构；每次构建都会更新 dist/qbmcp.exe。

只编译 exe、不打压缩包时：

```powershell
go build -trimpath -ldflags '-s -w' -o .\dist\qbmcp.exe ./cmd/qbmcp
if ($LASTEXITCODE -ne 0) { throw 'Build failed; stop here.' }
```

根目录不再包含 main.go，旧的 go build ... . 命令不再适用。

## 2. 解压并核对发布包

建议用压缩包验证实际分发和安装过程：

```powershell
$qbArchive = 'E:\Code\qbmcp\dist\qbmcp_0.3.1_windows_amd64.zip'
$qbExpectedHash = ((Get-Content -LiteralPath "$qbArchive.sha256" -Raw).Trim() -split '\s+')[0]
$qbActualHash = (Get-FileHash -LiteralPath $qbArchive -Algorithm SHA256).Hash
if ($qbActualHash -ne $qbExpectedHash) { throw 'Package checksum mismatch.' }

$qbRelease = Join-Path 'E:\Code\qbmcp\dist' ('manual-install-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
Expand-Archive -LiteralPath $qbArchive -DestinationPath $qbRelease
Get-ChildItem -LiteralPath $qbRelease
```

包根目录包含 qbmcp.exe、install.ps1、README.md 和 docs/。安装器从一份 exe 生成 qbmcp-background.exe，无需自行复制后台 exe，也不需要分发 scheduler.ps1。

已有发布包时可以跳过源码构建，将 $qbRelease 设为实际解压目录。

## 3. 确认实例并备份

默认实例不要设置 QBMCP_HOME。若以前使用自定义数据目录，先将该变量设为原目录。备份、停止、安装和启动必须使用同一配置。

```powershell
if ($env:QBMCP_HOME) {
    $qbData = [IO.Path]::GetFullPath($env:QBMCP_HOME)
    $qbBin = Join-Path $qbData 'bin'
} else {
    $qbData = Join-Path $env:LOCALAPPDATA 'qbmcp'
    $qbBin = Join-Path $env:LOCALAPPDATA 'Programs\qbmcp'
}
$qbInstalled = Join-Path $qbBin 'qbmcp.exe'
if (-not (Test-Path -LiteralPath $qbInstalled)) { throw 'No existing installation; use the first-install section.' }

$qbBeforeJSON = & $qbInstalled status --json
if ($LASTEXITCODE -ne 0) { throw 'Cannot query the existing installation.' }
$qbBefore = ($qbBeforeJSON -join [Environment]::NewLine) | ConvertFrom-Json
$qbBefore | Select-Object version, state, port, autostart, executable, config_path

$qbBackup = Join-Path 'E:\Code\qbmcp\dist' ('backup-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
New-Item -ItemType Directory -Path $qbBackup | Out-Null
Copy-Item -LiteralPath $qbInstalled -Destination (Join-Path $qbBackup 'qbmcp.exe')
Copy-Item -LiteralPath (Join-Path $qbData 'config.json') -Destination (Join-Path $qbBackup 'config.json')
$qbBeforeJSON | Set-Content -LiteralPath (Join-Path $qbBackup 'status-before.json') -Encoding UTF8
Write-Host "Backup: $qbBackup"
```

保留备份直到新版验证通过。备份配置可能含旧版凭据，请保存在自己的本地目录。

## 4. 停止旧版

```powershell
& $qbInstalled stop
if ($LASTEXITCODE -ne 0) { throw 'Stop failed; do not overwrite the installation.' }

& $qbInstalled status
if ($LASTEXITCODE -ne 0) { throw 'Status query failed.' }
```

应显示 stopped，后台 PID 为 0。stop 阻止当前开机周期内的恢复任务重新拉起旧进程，登录启动设置仍保留。网页和 MCP 会话会断开。

不要直接结束进程后立即复制文件，异常恢复任务可能重新启动旧版。覆盖安装也不需要先 uninstall。

## 5. 覆盖安装

执行解压目录中的新版安装脚本：

```powershell
& (Join-Path $qbRelease 'install.ps1')
```

此处不加 -Enable 或 -Start，方便逐步检查。脚本将：

1. 使用包中的新版 exe 执行 install。
2. 更新命令行 exe，并重新生成无控制台的后台 exe。
3. 更新任务执行路径，保留登录启动设置。
4. 保存端口和 allowed_origins，移除旧 token 等废弃字段。
5. 更新用户 PATH 和当前 PowerShell 的 PATH。

直接从源码构建产物覆盖时，改为：

```powershell
Set-Location 'E:\Code\qbmcp'
.\scripts\install.ps1 -BinaryPath '.\dist\qbmcp.exe'
```

两种方式选一种。正常更新无需再 enable，无需手动复制文件到安装目录。

若执行策略阻止脚本，仅为该次脚本进程使用：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $qbRelease 'install.ps1')
if ($LASTEXITCODE -ne 0) { throw 'Install failed.' }
```

独立进程不能刷新父窗口 PATH；后续使用 $qbInstalled 绝对路径仍可继续。其他已打开终端或客户端需重新打开以取得新 PATH。

## 6. 启动和验证

```powershell
& $qbInstalled start
if ($LASTEXITCODE -ne 0) { throw 'New version failed to start.' }

$qbAfterJSON = & $qbInstalled status --json
if ($LASTEXITCODE -ne 0) { throw 'New status query failed.' }
$qbAfter = ($qbAfterJSON -join [Environment]::NewLine) | ConvertFrom-Json

if ($qbAfter.version -ne '0.3.1' -or $qbAfter.health.version -ne '0.3.1') {
    throw 'The command or running background process is not version 0.3.1.'
}
if ($qbAfter.state -ne 'running') { throw 'Background process is not running.' }
if ($qbAfter.port -ne $qbBefore.port) { throw 'Port changed unexpectedly.' }
if ($qbAfter.autostart -ne $qbBefore.autostart) { throw 'Login startup setting changed unexpectedly.' }

& $qbInstalled status
```

命令版本和运行版本都应为 0.3.1，端口和登录启动与备份一致，安装路径正确。配置应仅包含 port 和 allowed_origins。

网页尚未连接时，page_connected=false、ready=false、tool_count=0 是正常结果。重新连接自己的网页并主动注册工具，再重新连接 MCP 客户端：

- MCP 只配置 status 显示的 /mcp 地址，不需要认证请求头或认证环境变量。
- 网页 Origin 必须在 allowed_origins 中；修改配置后需要 stop → start。
- 网页注册的工具应出现在客户端中，调用返回网页执行结果。
- 原来的 /demo 示例页和两个内置通用工具已经移除。
- 网页更新、清空、断开后，客户端工具列表应随之变化。

## 7. 回退

需要恢复备份版本时，在同一 PowerShell 窗口执行：

```powershell
& $qbInstalled stop
if ($LASTEXITCODE -ne 0) { throw 'Stop failed; do not continue rollback.' }

Copy-Item -LiteralPath (Join-Path $qbBackup 'config.json') -Destination (Join-Path $qbData 'config.json') -Force
& (Join-Path $qbBackup 'qbmcp.exe') install
if ($LASTEXITCODE -ne 0) { throw 'Rollback installation failed.' }

& $qbInstalled start
if ($LASTEXITCODE -ne 0) { throw 'Rollback start failed.' }
& $qbInstalled status
```

必须一并恢复旧配置，旧版可能仍要求凭据；对应客户端也需要恢复旧认证配置。安装器会重新生成备份版本的后台 exe。

## 首次安装

没有已有实例时，解压后在包目录执行：

```powershell
.\install.ps1 -Enable -Start
qbmcp status
```

- -Enable 开启登录启动。
- -Start 在安装后启动。
- 两项都省略则只安装，不立即启动、不默认开启登录启动。

## 常见问题

| 现象 | 处理 |
|---|---|
| 找不到命令 | 使用安装 exe 的绝对路径，或重新打开终端 |
| 文件正在使用 | 用已安装命令 stop；若仍报占用，核对 status 和相关进程，不要强行覆盖 |
| 端口被占用 | 查看 status 失败信息；释放端口，或 stop 后使用 start --port 新端口 |
| 版本仍旧 | 同时检查 version 与 health.version；确认使用包中的安装脚本并重新启动 |
| 工具为空 | 服务只接受网页提供的工具，检查网页连接和注册 |
| WebSocket 403 | 核对网页 Origin、主机名和端口，修改配置后重启 |
| 每分钟出现窗口 | 确认任务执行安装目录中的 qbmcp-background.exe，按 stop → install → start 更新 |

qbmcp uninstall 停止并删除当前实例任务及用户 PATH 条目，保留程序、配置和日志。覆盖安装不需要执行卸载。
