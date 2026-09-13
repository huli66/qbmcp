# 源码阅读指南

从一条命令如何启动程序、一个网页工具调用如何完成这两条路径开始阅读。

## 目录与职责

```text
cmd/qbmcp/main.go             命令入口，只负责错误输出和退出码
internal/
  cli/cli.go                 参数解析，将命令交给 Windows 后台管理
  config/config.go           版本、默认端口、路径、配置读写和校验
  server/
    server.go                HTTP 路由、MCP 会话、网页工具发布
    result.go                MCP 参数解码、结果和错误格式
    health.go                健康检查、关闭连接
  bridge/
    protocol.go              WebSocket 消息、工具定义、错误、请求 ID
    bridge.go                当前网页、工具注册表、连接替换、状态快照
    schema.go                JSON Schema 编译与验证规则
    queue.go                 FIFO 队列、调用、超时、取消、结果校验
    websocket.go             hello 握手、消息分派、心跳
  host/
    manager_windows.go       install/start/stop/enable/disable/uninstall
    install_windows.go       用户目录权限、准备配置、安装程序
    image_windows.go         生成无控制台后台 exe、替换程序文件
    path_windows.go          用户 PATH 增删
    scheduler_windows.go     调用 Windows PowerShell 任务管理
    scheduler.ps1            任务计划程序 COM 操作，编译时嵌入 exe
    runtime_windows.go       后台进程的启动、HTTP 服务、退出
    process_windows.go       运行记录和 PID/创建时间校验
    signals_windows.go       系统互斥锁和停止事件
    recovery_windows.go      本次开机的运行意图、停止标记、恢复检查
    status_windows.go        状态查询、文本/JSON 输出、就绪检测
    unsupported.go           非 Windows 平台的明确错误
  fileutil/atomic.go         文件原子替换及 JSON 写入
tests/                       全部测试和测试辅助函数
scripts/                     构建打包、安装脚本
docs/                        实现流程、协议、安装、测试说明
dist/                        生成的 exe、压缩包和校验文件，不提交 Git
```

包的依赖方向为：`cmd → cli → host → server → bridge`；配置供 host/server 使用，fileutil 供配置和 Windows 文件操作使用。bridge 不依赖 MCP SDK 或 Windows 后台管理，可以单独研究网页通信。

## 代码约定

- 根目录不放业务源码和测试，新增实现按职责放入 internal 对应包。
- cmd 只保留入口；下层包不反向依赖命令解析或 Windows 管理。
- Windows 专用实现使用 _windows.go 文件名，其他平台通过构建约束提供明确的不支持错误。
- 全部测试集中在 tests，按功能命名；公共测试辅助函数留在 helpers_test.go。
- 保持一个 Go 模块，不复制源码来运行测试。使用 gofmt，导入分为标准库、第三方和项目内部三组。
- 通用文件操作复用 fileutil；网页功能以注册工具扩展，不在服务端加入固定业务工具。

## 从 start 到后台就绪

1. `main.go → cli.Run` 解析命令、端口和内部启动参数。
2. `host.Manage` 取得管理互斥锁，检查当前进程和任务状态；已运行时直接报告就绪。
3. `PrepareConfig` 创建用户目录并保存配置。安装流程读取当前 exe，在同一安装目录生成命令行程序和 GUI 子系统的后台程序。
4. `scheduler("ensure")` 维护三项任务：主任务运行后台程序，logon 任务处理登录启动，watch 任务每分钟检查恢复。
5. start 清除本次开机的停止/失败标记，设置运行意图，并要求任务计划程序启动主任务。
6. 主任务进入 `RunBackground`，取得进程互斥锁、建立日志、校验停止标记，再监听配置端口。
7. `server.New` 创建 MCP 服务和网页桥接；后台写入 running 状态后，命令端通过健康接口确认就绪。

任务直接运行后台 exe。PowerShell 只处理任务管理，不承载常驻 HTTP 服务。

## 从网页注册到 MCP 调用

1. 网页连接 /ws，在 5 秒内发送 hello。服务返回随机 connectionId，完成协议握手。
2. 新连接替换旧网页，旧工具立即清空。网页随后主动发送 register_tools 完整快照。
3. `schema.go` 编译输入和输出 schema。全部成功后，`Bridge.register` 替换注册表并推进 schemaVersion。
4. 注册回调进入 `server.publish`，把网页定义发布为 MCP 工具并发送列表变更通知。未注册时没有任何工具。
5. MCP tools/call 进入 server 的工具处理函数：先解码参数，再调用 `Bridge.Invoke`。
6. Invoke 按当前 schema 校验参数，保存连接和 schema 版本，将请求放入全局 FIFO 队列。
7. worker 每次取一个请求，检查取消/连接/版本，发送 call_tool，再等待该请求的结果。
8. 网页返回 tool_result，经 connectionId 和请求 ID 路由、输出 schema 校验后，转换成 MCP 结果返回调用方。

等待队列最多 100 个，另有 1 个执行中的请求；默认期限 30 秒，从入队开始计算。请求不自动重试。

## 退出和恢复

- 网页断开：清除注册表、通知客户端；原调用收到断开错误。
- 新网页接管：旧连接关闭码为 4001，旧页面应停止自动重连。
- 主动 stop：记录本次开机的停止标记，取消恢复意图并发出 Windows 停止事件。
- 异常退出：watch 只在当前开机周期仍要求运行、且没有主动停止或确定性失败时恢复。
- 配置错误或端口占用：记录 failed 状态，等待用户处理，避免持续失败重启。
- enable/disable 只改变登录任务，不重启当前后台进程。

## 并发边界

bridge 的互斥锁保护当前连接、schema 和请求路由。注册回调在该锁内执行，不能重新进入 bridge。server 另用注册表锁保证 tools/list 读取完整快照。网页写操作由连接级写锁串行执行。

Windows 管理命令和后台运行分别使用系统互斥锁；Go 取得 Win32 互斥锁期间固定到同一系统线程。PID 必须同时匹配创建时间，避免误操作复用相同 PID 的其他进程。

## 本次清理

- 根目录只保留模块文件、README 和仓库配置。
- 全部 *_test.go 集中在 tests，通过正式模块接口和真实 HTTP/WebSocket 协议验证行为；无测试专用导出别名、复制源码或构建覆盖层。
- 文件、JSON、停止标记和程序安装的临时写入统一到 fileutil。
- 删除未使用的已编译工具定义副本、未消费的 pageId 字段和整数转字符串包装函数。
- token 验证、示例页面、示例工具和内置通用工具继续保持移除。
