# qbmcp

Go 编写的用户级 Windows 后台 MCP 程序。多个本地 agent 通过 MCP 连接，一个网页通过 WebSocket 提供 API 数据和 DOM 操作工具。

**v0.2 默认使用当前用户计划任务，安装、启停、登录启动及完整状态查询均无需管理员权限。** 从 v0.1 Windows 服务迁移时，仅停用旧系统服务需要一次管理员权限。

## 安装和快速开始

需要 Windows 10/11、系统自带的 Windows PowerShell 5.1 和任务计划程序服务。运行时无需 Go、Node.js 或第三方守护程序。编译需要 Go 1.24.3 或更高版本。

```powershell
go build -trimpath -ldflags "-s -w" -o dist/qbmcp.exe .
# 普通 PowerShell 中安装；已使用旧服务的用户先看下方迁移步骤
.\install.ps1 -Enable -Start
qbmcp help
qbmcp status
```

安装脚本复制 exe、添加用户 PATH，并刷新当前 PowerShell 的 PATH；其他已经打开的终端/应用需要重新打开。也可以直接运行：

```powershell
.\dist\qbmcp.exe install
# 重新打开普通终端
qbmcp enable
qbmcp start
```

如果执行策略阻止本地脚本，可以仅对本次脚本进程使用：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -Enable -Start
# 此方式无法修改父终端环境，结束后请重新打开终端
```

安装脚本不带参数时只安装，不立即启动、不默认开启登录启动。安装目录和运行数据分开：

| 内容 | 默认位置 |
|---|---|
| 全局命令 | `%LOCALAPPDATA%\Programs\qbmcp\qbmcp.exe` |
| 配置/token | `%LOCALAPPDATA%\qbmcp\config.json` |
| 滚动日志 | `%LOCALAPPDATA%\qbmcp\logs\qbmcp.log` |
| 运行状态 | `%LOCALAPPDATA%\qbmcp\runtime.json` |

打开 [测试页面](http://127.0.0.1:32300/demo)，从配置文件取得 token，粘贴并连接。页面显示工具已就绪后，agent 可以调用 `demo_load_data` 和 `demo_set_text`。

### 从 v0.1 Windows 服务迁移

新版检测到旧的 `qbmcp` 系统服务时，拒绝默认安装/启动，避免两套程序争用端口。迁移脚本只针对旧版标准安装路径，不会操作同名的其他程序。

1. 用**同一 Windows 账户的管理员 PowerShell**执行：

```powershell
.\migrate-service.ps1
```

2. 关闭管理员终端，在**普通 PowerShell**中执行：

```powershell
.\install.ps1 -Enable -Start
```

迁移脚本将旧配置和 token 复制到用户数据目录，禁用、停止并注销旧服务；保留 ProgramData 中的原程序、配置及日志。用户目录若已有不同的 config.json，会停止迁移，避免覆盖已有配置；相同配置允许中断后重试。迁移保持原端口、token 和 Origin 设置，MCP URL 和凭据通常无需修改。

如果迁移中断，先核对脚本输出、旧服务状态及两份配置；不要盲目删除文件。旧服务被标记删除后，如果新安装仍提示存在旧服务，请关闭“服务”管理窗口及旧版状态查询程序后再试。

## 命令与后台生命周期

| 命令 | 行为 |
|---|---|
| `install` | 安装/更新用户目录中的程序，注册计划任务并加入用户 PATH；保留已有登录启动设置 |
| `start [--port N]` | 首次自动准备配置和任务，然后由计划任务启动隐藏后台进程；重复执行不增加进程 |
| `stop` | 正常结束当前进程，取消当前任务运行，保留登录启动配置 |
| `enable [--port N]` | 添加当前用户登录触发器，不立即启动、不重启当前进程 |
| `disable` | 移除登录触发器，不停止当前进程，也不关闭异常恢复 |
| `status [--json]` | 显示用户任务、进程、端口、网页、工具和 MCP 会话状态 |
| `uninstall` | 停止程序、删除本用户任务、移除本安装目录的 PATH 条目；保留程序文件、配置和日志 |
| `help` | 显示帮助 |

- 后台进程由 Windows 任务计划程序发起，与启动它的终端和 agent 生命周期分离。主任务负责运行，独立的 `-logon` 小任务仅在登录时启动主任务；`-watch` 任务每分钟检查是否需要恢复退出的进程。enable/disable 只调整登录任务，不重新注册运行中的主任务。
- 任务以当前用户的普通权限运行，不保存账户密码，不请求最高权限；用户未登录时不会运行。
- exe 由隐藏 PowerShell 包装进程同步执行。工作进程监视包装进程：包装进程异常结束时，工作进程也会退出，防止留下无人管理的进程。
- 任务不设置最长运行时间，使用电池时继续运行；Windows 休眠期间仍无法处理请求。
- 工作进程或包装进程异常退出后，独立定时任务在下一次约一分钟的检查中恢复进程。主任务同时配置了 Windows 原生失败重试，但实际恢复不只依赖它。只有本次 Windows 启动中用户要求运行，才允许恢复；检查任务自身退出后，下一次定时触发仍可继续检查。
- 端口冲突、配置错误等确定性启动失败记录到日志和 status，并正常退出，不进行持续失败重试。
- disable 后定时检查任务仍保留，以便恢复本次手动运行的进程；重新启动 Windows 后旧运行意图失效，没有登录启动任务就不会启动 MCP 进程。uninstall 会移除主任务及两个辅助任务。
- `stop` 写入与本次 Windows 启动绑定的停止标记，阻止已排队的恢复重新提供服务。`start` 清除标记；重新启动 Windows 后旧标记失效，已 enable 的任务在用户登录时启动。同一次 Windows 启动内退出账户再登录仍保留主动停止状态。
- 管理命令与后台进程分别使用用户及数据目录隔离的系统互斥锁；所有路径、任务名称和内核对象按用户隔离。
- `_run --data-dir ...` 是计划任务内部入口，日常请使用 start。
- PATH 只修改当前用户，去重并保留其他条目；uninstall 只移除本程序的确切目录。
- 程序与配置目录仅当前用户、管理员和 SYSTEM 可访问，不再依赖 LocalService 或 ProgramData 权限。

更新版本：

```powershell
qbmcp stop
.\install.ps1
qbmcp start
```

更换端口：

```powershell
qbmcp stop
qbmcp start --port 32301
qbmcp status --json
```

运行中拒绝修改端口；不指定端口则保留当前配置。更换后同步修改网页地址和 MCP URL。

### 隔离测试目录

设置 `QBMCP_HOME` 可选择独立数据目录；其程序安装到该目录的 bin，计划任务名称也随目录变化。这用于隔离测试或多配置，不与默认实例共享状态。不要为两个实例配置同一端口。测试目录名称支持空格、中文和单引号。

## 配置与 Codex 接入

配置文件为 `%LOCALAPPDATA%\qbmcp\config.json`：

```json
{
  "port": 32300,
  "token": "初始化自动生成的64位十六进制字符串",
  "allowed_origins": []
}
```

- 默认仅监听 `127.0.0.1:32300`，HTTP、MCP、WebSocket 和演示页面共用端口。
- token 为 32 字节密码学随机值。网页通过第一条 hello 消息认证，MCP 使用 Bearer token；凭据不放在 URL、源码或日志中。
- 默认允许本服务 localhost/127.0.0.1 对应端口的 HTTP Origin；额外开发页面通过 allowed_origins 显式加入，例如 `http://localhost:5173`。
- 不允许通配符、空 WebSocket Origin、file:// 页面或远程 Origin。Host 同样校验。
- 配置修改后重启。修改 token 后更新网页和 agent 的凭据。
- 日志每份 5 MiB、最多 3 个备份，保留 14 天并压缩。
- 首版不包含 HTTPS/TLS 适配、远程访问、stdio 桥接和多网页路由。

将以下内容合并到 Codex config.toml：

```toml
[mcp_servers.qbmcp]
url = "http://127.0.0.1:32300/mcp"
bearer_token_env_var = "QBMCP_TOKEN"
tool_timeout_sec = 45
```

普通用户现在可以读取自己的配置：

```powershell
$qbConfig = Get-Content "$env:LOCALAPPDATA\qbmcp\config.json" -Raw | ConvertFrom-Json
$env:QBMCP_TOKEN = $qbConfig.token
# 从继承该环境变量的环境启动 Codex
```

已经运行的应用不会自动取得这个终端设置的变量。设置后重连 MCP，必要时重新启动客户端。[Codex 官方 MCP 文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)

## MCP 工具

服务采用[官方 Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)，使用有状态 Streamable HTTP，多客户端共享当前网页工具集合，各自拥有独立 MCP 会话。空闲会话超时为 10 分钟，客户端可以重新建立会话。

始终提供：

| 工具 | 参数 | 行为 |
|---|---|---|
| `qbmcp_discover_tools` | `{}` | 向网页请求最新 schema，等待注册成功后返回完整列表；未连接时立即报错 |
| `qbmcp_call_tool` | `{"name":"工具名","arguments":{}}` | 使用当前 schema 校验后转发给网页 |

网页连接后还会暴露其动态工具。注册、更新、断开会触发 `notifications/tools/list_changed`；不能刷新动态列表的客户端可始终使用通用入口。[MCP 工具规范](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)

网页工具名称必须匹配 `[a-zA-Z0-9_.-]{1,128}`，不得以 `qbmcp_` 开头。最多 256 个工具。schema 根类型必须是 `object`；支持 JSON Schema 校验及文档内部引用，禁止从网络或文件加载外部 `$ref`。

返回值以 JSON 文本放入 MCP `content`；JSON 对象同时作为 `structuredContent` 返回。执行失败使用 `isError: true`，并返回 `{"error":{"code":"...","message":"..."}}`。声明了 outputSchema 时也校验结果。

## 网页 WebSocket 协议 v1

连接 `ws://127.0.0.1:<port>/ws`。所有消息是 JSON，携带 `version: 1`。收到 `hello_ack` 后，网页发出的每条消息均携带服务分配的 `connectionId`。

### 1. 认证

网页必须在 5 秒内发送：

```json
{"version":1,"type":"hello","token":"<token>","pageId":"my-page"}
```

服务返回：

```json
{"version":1,"type":"hello_ack","connectionId":"<随机连接ID>"}
```

认证失败关闭码为 `4003`。未认证连接不能替换当前网页。

### 2. 注册和发现

网页主动发送完整工具快照；`tools: []` 表示清空网页工具：

```json
{
  "version": 1,
  "type": "register_tools",
  "connectionId": "<连接ID>",
  "tools": [{
    "name": "demo_set_text",
    "description": "修改演示区域文字",
    "kind": "dom",
    "inputSchema": {
      "type": "object",
      "properties": {"text": {"type": "string"}},
      "required": ["text"],
      "additionalProperties": false
    },
    "outputSchema": {
      "type": "object",
      "properties": {"text": {"type": "string"}},
      "required": ["text"]
    }
  }]
}
```

`kind` 只能为 `api` 或 `dom`，属于网页桥接元数据，不混入 MCP 标准工具字段。

全部 schema 校验成功后原子替换工具快照，返回：

```json
{"version":1,"type":"register_ack","connectionId":"<连接ID>","schemaVersion":2}
```

非法更新返回 `error` 并保留原有有效 schema。每次成功注册推进 schemaVersion。

调用发现工具时，服务发送 `request_tools`：

```json
{"version":1,"type":"request_tools","id":"<请求ID>","connectionId":"<连接ID>","schemaVersion":2}
```

网页回复同样的 `register_tools` 完整快照，并原样带上该 `id`。服务完成注册后才向发现调用返回结果。

### 3. 调用、结果和取消

```json
{"version":1,"type":"call_tool","id":"<请求ID>","connectionId":"<连接ID>","schemaVersion":2,"name":"demo_set_text","arguments":{"text":"Hello MCP"}}
```

网页查找预注册 handler 执行，然后返回：

```json
{"version":1,"type":"tool_result","id":"<请求ID>","connectionId":"<连接ID>","result":{"text":"Hello MCP"}}
```

失败时改用 `error` 字段：

```json
{"version":1,"type":"tool_result","id":"<请求ID>","connectionId":"<连接ID>","error":{"code":"PAGE_ERROR","message":"页面操作失败"}}
```

服务超时或收到取消时发送：

```json
{"version":1,"type":"cancel","id":"<请求ID>","connectionId":"<连接ID>"}
```

其他协议错误使用 `type: "error"`、关联 `id` 和结构化 `error`。

### 4. 并发、替换和限制

- 新网页认证成功后立即替换旧网页；旧连接关闭码 `4001`，必须停止自动重连，直到用户手动连接。
- 所有旧工具随连接失效而清除；新网页需要重新注册，不继承旧工具。
- 原网页未完成的调用收到 `PAGE_REPLACED` 或 `PAGE_DISCONNECTED`；迟到结果不会交付给其他请求。
- 每个桥接请求使用独立随机 ID，不使用 agent 的 JSON-RPC ID 做全局路由。
- 所有网页请求通过全局 FIFO 串行发送，等待队列最多 100 个，另有 1 个正在执行。
- 默认超时 30 秒，从入队开始计算；排队中被取消的调用不会发给网页。
- 注册表变化后，尚未发送的旧版本调用报 `SCHEMA_CHANGED`；已经发送的调用按原 outputSchema 验证结果。
- 超时、取消、断线均不自动重试。取消是尽力而为，不会撤销已产生的网页副作用；网页 handler 也应串行处理并支持 AbortSignal。
- WebSocket 收到的单条消息上限 4 MiB，超出后以 `1009` 关闭；MCP 请求体同样限制为 4 MiB。
- 服务每 15 秒发送 ping，45 秒未收到 pong 则断开。浏览器自动处理 WebSocket ping/pong。
- 示例页网络断线后从 1 秒开始指数退避，最长 30 秒；认证失败和被替换不会自动重连。

主要错误码：`PAGE_NOT_CONNECTED`、`PAGE_DISCONNECTED`、`PAGE_REPLACED`、`SERVICE_STOPPED`、`UNKNOWN_TOOL`、`INVALID_SCHEMA`、`INVALID_ARGUMENTS`、`INVALID_RESULT`、`INVALID_MESSAGE`、`SCHEMA_CHANGED`、`QUEUE_FULL`、`TIMEOUT`、`CANCELED`。

## 健康检查

`GET /health` 无需 token，只返回最小运行状态，不包含网页 URL、token、工具参数或结果。

```json
{
  "service": "running",
  "version": "0.2.0",
  "pid": 12345,
  "uptime_seconds": 120,
  "address": "127.0.0.1:32300",
  "page_connected": true,
  "ready": true,
  "tool_count": 2,
  "mcp_sessions": 2,
  "pending_requests": 0
}
```

`tool_count` 只统计网页工具，不包含两个内置工具。`ready` 表示当前连接已成功注册工具快照（允许空快照）。网页未连接仍返回 HTTP 200，但 `page_connected`、`ready` 为 false。

`status` 查询当前用户的计划任务、进程创建时间和健康接口，区分未安装、已停止、启动中、运行中、等待恢复及启动失败。PID 与创建时间共同校验，避免 PID 被复用时误认其他程序；健康接口的 PID 也必须匹配。

## 示例页面

`web/demo.html` 是可直接修改的原生 HTML/JavaScript，编译后在 `/demo` 提供。

- `demo_load_data`：页面通过 fetch 请求 `/demo/api/data`，将返回的数据交给 MCP。
- `demo_set_text`：使用 `textContent` 修改演示区域，返回修改后的文本。
- 提供 token 输入、连接/断开、状态、工具定义和最多 100 条请求日志。
- 应通过服务访问 `/demo`，不要直接用 `file://` 打开；后者的 Origin 不符合默认限制。

## 实施进度与验证记录

本 README 继续作为后续 goal 模式的验收依据。v0.2 已将 Windows 系统服务替换为当前用户计划任务，MCP 和网页协议保持兼容。

- [x] Go 模块、配置、日志、单 exe 嵌入 HTML。
- [x] 网页认证、动态 schema、单网页替换、FIFO 队列、取消和超时。
- [x] MCP 两个稳定入口、动态工具、多客户端与变更通知。
- [x] 用户目录安装、用户 PATH、任务管理、登录触发器和进程恢复实现。
- [x] 旧 Windows 服务迁移脚本，保留原配置。
- [ ] 完成下列真实系统和客户端验收后，标记整体目标完成。

2026-09-13，Windows amd64 / Go 1.24.3：

- [x] Go 单元/集成测试：MCP、WebSocket、并发请求路由、schema、限流、取消、超大消息等原有测试通过。
- [x] 用户路径和配置、PATH 去重/移除、互斥锁串行化、PID 创建时间及停止标记测试通过。
- [x] 真实普通用户（非管理员）安装、PATH 去重、启停、登录开关、工作进程崩溃恢复、主动停止超过一分钟不重启、端口占用报错及卸载清理：通过（TestUserTaskLifecycle，136.76 秒）。原用户 PATH 恢复，独立测试任务已删除。
- [x] 包装进程终止后没有遗留孤立工作进程，约一分钟后恢复：通过（TestUserWrapperRecovery，63.85 秒）。两项真实系统测试合计 201.235 秒。
- [x] 最终 Windows 构建、go vet、Go 回归测试、4 项网页逻辑测试及三个 PowerShell 脚本语法检查：通过。
- [ ] 真实旧服务迁移：需要一次管理员运行迁移脚本；本次开发未自动停用现有旧服务。
- [ ] 实际重新登录/重启 Windows，验证登录启动。
- [ ] 真实浏览器和 Codex 会话操作两个演示工具。
- [ ] CGO 竞态检查：此前环境无可用 GCC，需配置 Windows C 编译器后执行。

自动化验证：

```powershell
go test ./... -count=1 -timeout=60s
go vet ./...
node --test web/demo.test.cjs
# 需要兼容的 C 编译器：
$env:CGO_ENABLED = '1'
go test -race ./...
```

可选的真实任务测试（普通用户运行，会创建独立临时任务和 PATH 条目并在结束时清理，耗时数分钟，不操作旧服务或默认实例）：

```powershell
go build -o dist/qbmcp.exe .
$env:QBMCP_LIFECYCLE_TEST = '1'
go test -run '^TestUser(TaskLifecycle|WrapperRecovery)$' -v -count=1 -timeout=7m
Remove-Item Env:QBMCP_LIFECYCLE_TEST
```

人工验收：

1. 普通终端安装，重新打开终端后直接运行 qbmcp help/status；确认任务以当前用户普通权限运行。
2. start 后关闭终端/agent，检查网页和后台连接仍保持；两个 agent 分别调用工具。
3. enable/disable 时 PID 不变；登录启动与异常恢复互相独立。
4. 用 status 确认本实例 PID 后测试异常退出，约一分钟恢复；主动 stop 后超过一分钟仍不恢复。
5. 更换端口后重启保留设置；端口被占用时明确失败而非反复重启。
6. enable、stop，再重启 Windows 并登录，应自动启动；disable 后重启登录不应启动。
7. 新网页接管后旧网页不自动抢回，旧请求不串线。
8. uninstall 后任务和本安装目录的 PATH 条目消失，用户配置仍保留。

## 排障

| 现象 | 检查 |
|---|---|
| 提示旧 Windows 服务存在 | 先按迁移步骤运行一次管理员脚本，再回到普通终端安装 |
| 找不到 qbmcp 命令 | 运行 install，并重新打开终端；其他应用可能也需重启以刷新 PATH |
| 计划任务操作被拒绝 | 检查系统任务计划程序服务及组织策略；不要改用管理员长期运行来掩盖权限问题 |
| 状态 recovering | 异常退出后的恢复等待期；约一分钟后检查，持续失败则查看日志 |
| 状态 failed | 查看 status 错误和用户目录日志，修复配置/端口问题后 start |
| 网页认证失败 | 检查用户配置中的 token，修改配置后重启 |
| WebSocket 403 | 通过 /demo 访问，检查 Origin，不要直接双击 HTML 使用 file:// |
| 新动态工具不可见 | 先 discover，再用 qbmcp_call_tool；必要时重新连接 MCP |
| 工具超时 | 保持页面活跃，检查网页/API 日志；操作可能已经发生，不要盲目重试 |
