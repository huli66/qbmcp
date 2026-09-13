# 验证与测试

所有 Go 测试位于 tests/，生产目录不含测试文件。Go 模块仍只有根目录的 go.mod，直接使用标准 go test。

## 本次验证记录

2026-09-14，Windows amd64、Go 1.24.3、普通用户：24 项测试全部通过，包含真实任务生命周期测试（137.58 秒）。独立实例完成旧构建安装、新发布包覆盖、文件及运行版本检查、端口/Origin/登录启动保留、崩溃恢复、主动停止和卸载清理。测试没有替换默认安装实例。

go vet、Windows 构建、发布包校验、安装/构建/任务脚本及文档中 PowerShell 命令的语法检查均通过。实际重启 Windows 后登录启动仍需按手动验收步骤确认；本次未运行需要 C 编译器的竞态检测。

## 常规检查

在仓库根目录执行：

```powershell
go test ./... -count=1 -timeout=60s
if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Static checks failed' }
.\scripts\build.ps1
```

Windows 权限和系统互斥锁测试需要普通用户的本机系统访问。受限沙箱可能返回 Access is denied；无需管理员权限。

| 文件 | 覆盖内容 |
|---|---|
| mcp_test.go | 无认证连接、初始空列表、动态工具、多客户端路由、列表通知、HTTP 边界 |
| bridge_test.go | 握手、替换、schema、队列、超时、取消、迟到结果、消息限制 |
| config_test.go | 配置校验、保留端口/Origin、移除废弃字段 |
| fileutil_test.go | 原子替换、序列化失败保留原文件、临时文件清理 |
| host_windows_test.go | 用户目录、配置、PATH、互斥锁、PID 创建时间 |
| background_windows_test.go | 后台 exe 的 GUI 子系统、重复生成、非法 PE 输入 |
| stop_windows_test.go | 停止标记、开机周期、运行意图和恢复前置条件 |
| status_windows_test.go | 无进程/配置异常时的状态和路径、输出字段 |
| lifecycle_windows_test.go | 可选真实任务安装、覆盖、恢复、停止和卸载 |
| helpers_test.go | MCP 客户端和模拟网页连接；不随程序打包 |

## 可选真实 Windows 生命周期测试

此测试使用临时 QBMCP_HOME、独立安装目录和任务名称，结束时删除测试任务与测试 PATH 条目。会暂时改动当前用户 PATH，耗时约 2–4 分钟。不会安装或停止默认实例。

```powershell
.\scripts\build.ps1
$env:QBMCP_LIFECYCLE_TEST = '1'
try {
    go test ./tests -run '^TestUserTaskLifecycle$' -v -count=1 -timeout=7m
    if ($LASTEXITCODE -ne 0) { throw 'Lifecycle test failed' }
} finally {
    Remove-Item Env:QBMCP_LIFECYCLE_TEST -ErrorAction SilentlyContinue
}
```

如需同时验证从旧 exe 升级，运行前把 QBMCP_PREVIOUS_BINARY 设为旧构建的绝对路径。测试会先安装旧版，再从新版发布压缩包中的 install.ps1 覆盖，并检查版本、端口、Origin、登录启动和已安装文件。

竞态检查需要兼容的 Windows C 编译器：

```powershell
$env:CGO_ENABLED = '1'
go test -race ./tests -count=1 -timeout=90s
```

## 手动验收

用自己的网页注册和调用工具；推送新快照、空快照、断开、重新接管，确认客户端列表和结果正确。登录启动的最终验收需要真正重启 Windows 并登录。详细升级步骤见 [安装文档](install.md)。
