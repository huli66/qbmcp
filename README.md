# qbmcp

用 Go 编写的 Windows 常驻 MCP 服务：多个本地 agent 通过 MCP 连接服务，一个网页通过 WebSocket 提供工具。网页负责请求业务接口、执行 DOM 操作，Go 负责工具注册、参数校验、排队和结果路由。

## 实施计划与当前状态

本文件是后续 goal 模式的实施和验收依据。代码已经实现首版功能；**自动化链路已验证，真实 Windows 服务恢复、开机启动和 Codex 会话仍需人工验收**，不应将这些项目视为已通过。

```text
Codex / 其他本地 agent（多个）
              │ MCP / Streamable HTTP
              ▼
      qbmcp Windows 服务
      ├─ MCP 工具注册与调用
      ├─ WebSocket 连接管理
      ├─ 内存 schema 与请求路由
      └─ /health 状态查询
              ▲
              │ WebSocket（唯一活动网页）
              │
         HTML / 业务网页
         ├─ 注册工具 schema
         ├─ fetch 接口并返回数据
         └─ DOM 操作并返回结果
```

### 目标和边界

- 编译为单个 `qbmcp.exe`，HTML 通过 Go embed 嵌入，无需 Node.js 或浏览器插件即可运行。
- agent 连接已经运行的服务，不负责启动或维持服务进程；agent 退出不影响网页连接。
- 网页主动连接并注册工具；服务不自动打开浏览器。
- 首版面向 Windows 10/11、本地 HTTP 测试页面及支持 Streamable HTTP 的本地 agent。
- 暂不包含 HTTPS 网站/TLS 证书、远程访问、stdio 桥接、多网页路由和任意 JavaScript 执行。
- 电脑休眠、浏览器关闭或页面冻结期间不能保证连接；恢复后需要重连并重新注册。

### 按阶段推进

- [x] **项目基础**：Go 模块、固定依赖版本、配置、滚动日志、命令入口、HTTP 路由。
- [x] **网页桥接**：认证、schema 校验、单网页替换、请求关联、FIFO 队列、取消与超时、示例页面。
- [x] **MCP 接入**：两个内置工具、动态工具、工具列表变化通知、多客户端集成测试。
- [x] **Windows 常驻实现**：服务注册、LocalService 身份、受保护安装目录、启停、开机启动和失败恢复代码，Windows 构建通过。
- [ ] **最终验收**：CGO 竞态检查、真实服务恢复、实际重启电脑、浏览器和 Codex 联调；按下方清单记录结果。

goal 完成标准：单个可执行文件通过全部验收场景，并在本文件记录真实验证结果。缺少环境的验收不能用模拟测试代替。

## 编译和快速开始

需要 Go 1.24.3 或更高版本。运行服务不需要安装 Go。

```powershell
go mod download
go test ./... -count=1
go vet ./...
go build -trimpath -ldflags "-s -w" -o dist/qbmcp.exe .
```

在**管理员 PowerShell** 中启动：

```powershell
.\dist\qbmcp.exe start
.\dist\qbmcp.exe enable
.\dist\qbmcp.exe status
```

首次注册会将程序复制到 `%ProgramData%\qbmcp\bin\qbmcp.exe`，Windows 服务固定使用该副本。服务账户为 `NT AUTHORITY\LocalService`。普通用户可以查询服务状态，但读取受保护配置并查询自定义端口的详细状态需要管理员权限。

1. 从 `%ProgramData%\qbmcp\config.json` 取得 `token`。
2. 打开 [本地测试页面](http://127.0.0.1:32300/demo)，粘贴 token，点击“连接”。
3. 页面显示“工具已就绪”后，配置 MCP 客户端。
4. 让 agent 调用 `demo_load_data`，或者调用 `demo_set_text` 修改页面文字。

### Codex 配置

将以下配置合并到 Codex 的 `config.toml`，不要覆盖原有配置：

```toml
[mcp_servers.qbmcp]
url = "http://127.0.0.1:32300/mcp"
bearer_token_env_var = "QBMCP_TOKEN"
tool_timeout_sec = 45
```

在启动 Codex 的环境中设置 `QBMCP_TOKEN`。例如，在能读取配置的终端内：

```powershell
$qbConfig = Get-Content "$env:ProgramData\qbmcp\config.json" -Raw | ConvertFrom-Json
$env:QBMCP_TOKEN = $qbConfig.token
# 此后从继承该环境变量的环境启动 Codex。
```

已经运行的桌面应用不会自动取得这个终端中新设置的环境变量。配置后重新连接 MCP，必要时重启客户端。

Codex 的 URL/Bearer token 配置依据[官方 MCP 接入文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)。其他 agent 使用相同的 MCP URL 和 `Authorization: Bearer <token>`。

## 命令和 Windows 生命周期

| 命令 | 行为 |
|---|---|
| `start [--port N]` | 未注册则注册为手动启动服务，然后启动；已经运行则幂等返回 |
| `stop` | 正常停止，不触发异常恢复；保留开机启动设置 |
| `enable [--port N]` | 未注册则注册，设置开机自动启动；不立即启动 |
| `disable` | 设置为手动启动；不停止服务，也不禁止手动 start |
| `status [--json]` | 查询 Windows 服务状态、启动类型、PID 和可取得的连接信息 |
| `help` | 显示帮助 |

- 使用 Windows SCM 保证服务单实例，管理命令通过系统互斥锁串行执行。
- 异常退出由 SCM 按 5 秒、15 秒、60 秒延迟恢复，此后重复 60 秒；无故障 24 小时后重置计数。
- 正常 `stop` 报告停止状态，不触发恢复。之前执行过 `enable` 时，下一次开机仍会启动。
- `stop` 还保存与本次 Windows 启动标识绑定的停止标记，阻止已经排队的失败恢复重新提供服务；`start` 清除标记，新一轮 Windows 启动自动忽略旧标记。
- 配置错误或端口占用会记录启动错误并正常报告失败，不进行无限失败重启。
- `enable/disable` 和进程失败恢复互相独立；disable 后手动启动的服务仍有异常恢复。
- 停止后从新版程序运行 `start` 会更新受保护的程序副本，再启动服务。不要在运行中覆盖已安装的可执行文件。
- `service` 是内部入口，仅供 SCM 调用，不能当作前台服务器运行。

修改端口：

```powershell
.\dist\qbmcp.exe stop
.\dist\qbmcp.exe start --port 32301
.\dist\qbmcp.exe status --json
```

随后同时修改网页地址和 agent 的 MCP URL。端口持久化，运行中不能修改端口。不传 `--port` 时保留已有设置。

## 配置和本地访问保护

配置文件：`%ProgramData%\qbmcp\config.json`。

```json
{
  "port": 32300,
  "token": "首次初始化自动生成的64位十六进制字符串",
  "allowed_origins": []
}
```

- 默认绑定 `127.0.0.1:32300`；MCP、WebSocket、健康检查及示例页面共用端口。
- token 是 32 字节密码学随机数。MCP 使用 Bearer token，网页在第一条 WebSocket 消息中发送 token。
- token 不放在 URL、HTML 源码或日志里。演示页面仅将其保存在当前页面内存。
- 默认允许 `http://127.0.0.1:<port>` 和 `http://localhost:<port>` 两个 Origin。WebSocket 不接受空 Origin、`null` 或通配符。
- 其他本地开发页面可以在 `allowed_origins` 添加完整 Origin，例如 `http://localhost:5173`；首版仅接受 localhost/127.0.0.1 的 HTTP Origin。
- 修改配置后重启服务。更换 token 后网页和 agent 都需要更新凭据。
- Host 仅接受配置端口的 localhost/127.0.0.1；非允许的浏览器 Origin 返回 403。
- 配置和程序目录仅管理员、SYSTEM 可写，LocalService 可读；日志目录另行授予 LocalService 写权限。
- 滚动日志位于 `%ProgramData%\qbmcp\logs\qbmcp.log`：每份 5 MiB，最多 3 个备份，保留 14 天并压缩。

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
  "version": "0.1.0",
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

`status` 先查询 SCM，再查询健康接口，明确区分“服务停止”“服务运行但健康接口不可达”和“网页未连接”。

## 示例页面

`web/demo.html` 是可直接修改的原生 HTML/JavaScript，编译后在 `/demo` 提供。

- `demo_load_data`：页面通过 fetch 请求 `/demo/api/data`，将返回的数据交给 MCP。
- `demo_set_text`：使用 `textContent` 修改演示区域，返回修改后的文本。
- 提供 token 输入、连接/断开、状态、工具定义和最多 100 条请求日志。
- 应通过服务访问 `/demo`，不要直接用 `file://` 打开；后者的 Origin 不符合默认限制。

## 验证记录与待办

2026-09-12，本地 Windows amd64 / Go 1.24.3：

- [x] `go test ./... -count=1 -timeout=60s`：通过。
- [x] `go vet ./...`：通过。
- [x] 编译 `dist/qbmcp.exe`：通过。
- [x] 可执行文件 help、未注册状态查询和非法端口提示：通过。
- [x] `node --test web/demo.test.cjs`：4 项通过，覆盖演示页握手、API/DOM handler、取消和重连；这是可选的开发测试，不是运行依赖，也不替代真实浏览器验收。
- [x] Windows 启动标识只读检查及停止标记生命周期测试：通过，未修改系统服务。
- [x] 无网页时只有两个内置工具，发现工具返回未连接错误。
- [x] 真实 HTTP/WebSocket + 官方 Go MCP 客户端集成链路，两客户端并发调用且结果不串线。
- [x] agent 先连接、网页后注册，接收到动态工具列表通知；动态入口与通用入口均可调用。
- [x] 关闭 MCP 客户端不影响网页连接；网页断开清除工具。
- [x] 新网页替换旧网页，未完成请求失败，旧连接收到 4001。
- [x] 错误 token/Origin 不替换已有网页；Host 校验、健康接口和演示源码不泄露 token。
- [x] 非法 schema、重复名称和保留名称保持旧工具快照；拒绝外部 schema 引用。
- [x] 非法参数、非法返回值、超时、取消、迟到结果、队列满、排队期间 schema 更新及 4 MiB 上限。
- [ ] `go test -race ./...`：当前 `CGO_ENABLED=0` 且未找到 GCC；需要安装支持 Go race 的 Windows C 编译器后设置 `CGO_ENABLED=1`、`CC` 并运行。
- [ ] 真实管理员服务测试：当前运行环境不是管理员，未注册或修改本机 Windows 服务。
- [ ] 真实浏览器操作两个演示工具，观察 API 数据和 DOM 结果。
- [ ] 真实 Codex 会话的工具刷新与通用入口回退。
- [ ] 实际重启电脑验证开机启动。

### Windows / Codex 人工验收步骤

在专用测试环境或确认没有正在使用的 qbmcp 服务后执行：

1. 管理员终端执行 `start`，检查 `/health`、`status --json`、服务账户和安装路径；重复 start 不增加进程。
2. 打开 `/demo` 并连接，配置 Codex；调用两类示例工具。再连接第二个 agent，核对各自结果。
3. 关闭所有 agent，确认网页和服务仍连接；再打开第二个测试页，确认第一个显示被替换且不自动抢回。
4. 执行 `enable`，确认当前 PID 不变；执行 `disable`，确认进程仍运行。
5. 保持 disable，强制结束**status 返回的 qbmcp 服务 PID**，确认约 5 秒后恢复；不要结束不属于本服务的进程。
6. 执行 stop，等待超过 60 秒，确认不恢复；另测试强制终止后在恢复等待期立即 stop，确认仍不恢复。再执行 start，确认可手动运行。
7. stop 后改端口并 start，确认新地址可用；服务运行时尝试修改端口，应明确报错。
8. stop 后用测试监听器占用配置端口再 start，确认正常报告启动失败且日志记录原因；释放端口后可正常启动。
9. 执行 enable，再 stop，重启 Windows，确认开机启动；执行 disable，再重启，确认不自动启动。
10. 把实际系统版本、客户端版本、执行日期和结果更新到本节，再勾选最终验收项。

### 排障

| 现象 | 检查 |
|---|---|
| 管理命令权限不足 | 使用管理员终端；普通用户状态查询可能无法读取配置 |
| 启动失败 | 查看滚动日志；确认配置合法、端口未占用 |
| 网页认证失败 | 核对当前 config.json 的 token；修改后需要重启服务 |
| WebSocket 403 | 使用服务 `/demo` 地址，检查 Origin 配置，不要用 file:// |
| 网页连不上本地服务 | 检查浏览器本地网络访问权限、服务状态和端口 |
| agent 看不到新增工具 | 先用 discover，再通过 qbmcp_call_tool 调用；必要时重连 MCP |
| 工具超时 | 保持页面活跃，检查页面日志及 API 请求；操作可能已发生，不应盲目重试 |
| 更新程序后仍是旧版本 | stop 后从新构建的 exe 执行 start，检查 /health 版本 |

核心代码按配置、桥接、HTTP/MCP、Windows 生命周期拆分；测试不需要安装 Windows 服务。后续扩展应继续保持服务生命周期独立于 agent，并保留两个稳定的内置工具。
