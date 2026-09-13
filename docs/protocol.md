# 网页接入与协议

## 配置与 MCP 接入

配置文件为 `%LOCALAPPDATA%\qbmcp\config.json`，仅包含端口和允许的网页 Origin：

```json
{
  "port": 32300,
  "allowed_origins": ["http://localhost:5173"]
}
```

- 默认仅监听 `127.0.0.1:32300`，MCP、WebSocket 和健康检查共用端口。
- 默认 `allowed_origins` 为空；服务自身 localhost/127.0.0.1 对应端口的 HTTP Origin 始终允许。其他本地网页需加入其确切 Origin，localhost 和 127.0.0.1 分别配置。
- 不允许通配符、空 WebSocket Origin、file:// 页面或远程 Origin。Host 同样校验。
- 配置修改后重启。旧配置中已废弃的字段会被忽略，下次安装或启动时保存配置会将其移除。
- 日志每份 5 MiB、最多 3 个备份，保留 14 天并压缩。
- 不包含 HTTPS/TLS 适配、远程访问、stdio 桥接和多网页路由。

MCP 客户端只需配置 Streamable HTTP 地址，不需要认证请求头或认证环境变量。先运行 `qbmcp start`，以 `qbmcp status` 输出的 MCP 地址为准。

| 字段 | 内容 |
|---|---|
| 名称 | `qbmcp` |
| 类型 | `Streamable HTTP` |
| URL | `http://127.0.0.1:32300/mcp` |

支持 TOML 配置的客户端可使用：

```toml
[mcp_servers.qbmcp]
url = "http://127.0.0.1:32300/mcp"
tool_timeout_sec = 45
```

升级旧客户端配置时，删除原先用于 qbmcp 的认证请求头和认证环境变量设置，重新连接 MCP。

## MCP 工具

服务采用[官方 Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)，使用有状态 Streamable HTTP。多个客户端共享当前网页工具集合，各自拥有独立 MCP 会话。空闲会话超时为 10 分钟，客户端可以重新建立会话。

服务不内置任何工具，也不提供示例网页、示例 API 或通用调用工具。工具定义及执行逻辑全部由网页提供：

- 网页未连接或尚未注册工具时，`tools/list` 返回空列表。
- 网页通过 `register_tools` 主动提交完整工具快照；注册成功后，MCP 直接暴露这些工具，客户端通过标准 `tools/call` 按名称调用。
- 网页更新工具时再次提交完整快照；空快照清除所有工具。
- 注册、更新、清空或断开导致的工具变更会触发 `notifications/tools/list_changed`。服务从首次 MCP 握手起就声明工具列表变更能力，即使当时工具为空。
- 客户端收到通知后应重新请求 `tools/list`。不支持自动刷新的客户端需重新连接 MCP。
- 网页断开或被替换后，其工具立即移除。健康检查中的 `tool_count` 与当前工具列表数量一致。

网页工具名称必须匹配 `[a-zA-Z0-9_.-]{1,128}`，没有保留前缀。最多 256 个工具。schema 根类型必须是 `object`；支持 JSON Schema 校验及文档内部引用，禁止从网络或文件加载外部 `$ref`。

返回值以 JSON 文本放入 MCP `content`；JSON 对象同时作为 `structuredContent` 返回。执行失败使用 `isError: true`，并返回 `{"error":{"code":"...","message":"..."}}`。声明了 outputSchema 时也校验结果。

## 网页 WebSocket 协议 v1

连接 `ws://127.0.0.1:<port>/ws`。所有消息是 JSON，携带 `version: 1`。收到 `hello_ack` 后，网页发出的每条消息均携带服务分配的 `connectionId`。

### 1. 连接握手

网页必须在 5 秒内发送：

```json
{"version":1,"type":"hello"}
```

服务返回：

```json
{"version":1,"type":"hello_ack","connectionId":"<随机连接ID>"}
```

hello 仅用于确认协议版本。格式或版本不正确时，以 WebSocket 协议错误码 `1002` 关闭连接；无效握手不能替换当前网页。

### 2. 网页主动注册

网页发送 `type: "register_tools"`，携带 `connectionId` 和 `tools` 数组。每个工具包含以下字段：

| 字段 | 要求 |
|---|---|
| `name` | 网页定义的唯一工具名称 |
| `description` | 工具用途说明 |
| `kind` | `api` 或 `dom`，属于网页桥接元数据，不混入 MCP 标准工具字段 |
| `inputSchema` | 根类型为 `object` 的 JSON Schema |
| `outputSchema` | 可选；提供时根类型为 `object`，用于校验返回结果 |

所有 schema 校验成功后原子替换工具快照，返回：

```json
{"version":1,"type":"register_ack","connectionId":"<连接ID>","schemaVersion":2}
```

非法更新返回 `error` 并保留原有有效 schema。每次成功注册推进 schemaVersion。网页必须主动推送更新，服务不再请求重新发现工具。

清空工具：

```json
{"version":1,"type":"register_tools","connectionId":"<连接ID>","tools":[]}
```

### 3. 调用、结果和取消

客户端调用已注册工具时，服务发送：

```json
{"version":1,"type":"call_tool","id":"<请求ID>","connectionId":"<连接ID>","schemaVersion":2,"name":"<网页注册的工具名>","arguments":{}}
```

网页按名称查找自己提供的 handler，使用 arguments 执行操作，原样带回请求 id：

```json
{"version":1,"type":"tool_result","id":"<请求ID>","connectionId":"<连接ID>","result":{}}
```

arguments 和 result 的内容由对应工具的 schema 决定。失败时改用 `error` 字段：

```json
{"version":1,"type":"tool_result","id":"<请求ID>","connectionId":"<连接ID>","error":{"code":"PAGE_ERROR","message":"页面操作失败"}}
```

服务超时或收到取消时发送：

```json
{"version":1,"type":"cancel","id":"<请求ID>","connectionId":"<连接ID>"}
```

其他协议错误使用 `type: "error"`、关联 `id` 和结构化 `error`。

### 4. 并发、替换和限制

- 新网页握手成功后立即替换旧网页；旧连接关闭码 `4001`，必须停止自动重连，直到用户手动连接。
- 所有旧工具随连接失效而清除；新网页需要重新注册，不继承旧工具。
- 原网页未完成的调用收到 `PAGE_REPLACED` 或 `PAGE_DISCONNECTED`；迟到结果不会交付给其他请求。
- 每个桥接请求使用独立随机 ID，不使用 agent 的 JSON-RPC ID 做全局路由。
- 所有网页调用通过全局 FIFO 串行发送，等待队列最多 100 个，另有 1 个正在执行。
- 默认超时 30 秒，从入队开始计算；排队中被取消的调用不会发给网页。
- 注册表变化后，尚未发送的旧版本调用报 `SCHEMA_CHANGED`；已经发送的调用按原 outputSchema 验证结果。
- 超时、取消、断线均不自动重试。取消是尽力而为，不会撤销已产生的网页副作用；网页 handler 也应串行处理并支持 AbortSignal。
- WebSocket 收到的单条消息上限 4 MiB，超出后以 `1009` 关闭；MCP 请求体同样限制为 4 MiB。
- 服务每 15 秒发送 ping，45 秒未收到 pong 则断开。浏览器自动处理 WebSocket ping/pong。
- 网络断线后的重连由网页实现；每次重新连接都需要握手并注册工具。

主要错误码：`PAGE_NOT_CONNECTED`、`PAGE_DISCONNECTED`、`PAGE_REPLACED`、`SERVICE_STOPPED`、`UNKNOWN_TOOL`、`INVALID_SCHEMA`、`INVALID_ARGUMENTS`、`INVALID_RESULT`、`INVALID_MESSAGE`、`SCHEMA_CHANGED`、`QUEUE_FULL`、`TIMEOUT`、`CANCELED`。

## 健康检查与状态

`GET /health` 返回运行状态，不包含网页 URL、工具参数或结果。

```json
{
  "service": "running",
  "version": "0.3.1",
  "pid": 12345,
  "uptime_seconds": 120,
  "address": "127.0.0.1:32300",
  "page_connected": false,
  "ready": false,
  "tool_count": 0,
  "mcp_sessions": 2,
  "pending_requests": 0
}
```

`ready` 表示当前连接已成功注册工具快照（允许空快照）。网页未连接仍返回 HTTP 200，但 `page_connected`、`ready` 为 false。

`status` 查询当前用户的计划任务、进程创建时间和健康接口，区分未安装、已停止、启动中、运行中、等待恢复及启动失败。PID 与创建时间共同校验，避免 PID 被复用时误认其他程序；健康接口的 PID 也必须匹配。

文本及 `status --json` 包含版本、PID、登录启动、任务及恢复检查状态、端口、监听地址、MCP/WebSocket/health URL，以及命令程序、后台程序、安装目录、数据目录、配置文件、日志文件和运行记录的绝对路径。程序停止时仍显示配置端口和路径；没有配置时注明默认端口。`port_source` 为 `runtime`、`config`、`default` 或 `unavailable`，运行中优先显示实际监听端口。

运行时另外显示运行版本、时长、网页连接、工具就绪、工具数、MCP 会话数和待处理请求数。健康接口不可达时明确显示“未查询到健康状态”。任务查询被拒绝时仍输出路径和配置详情，并以非零退出码报告错误。
