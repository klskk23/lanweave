# Data Model: OpenWrt 客户端基础 (031)

**无服务端 schema 变更、无新 API 端点**。涉及的数据全部在客户端本地。

## 1. 本地文件布局（`/etc/lanweave/`，`--data-dir` 可覆盖）

| 路径 | 内容 | 权限 | 属主 |
|---|---|---|---|
| `state.json` | `state.Record`（非机密状态） | 0600 | daemon 与 CLI 共写（原子写） |
| `keys/device_private` | 设备 WG 私钥 | 0600（目录 0700） | onboard 写入、daemon 读、logout 删 |
| `keys/session_token` | access token | 0600 | apiclient 会话往返 |
| `keys/refresh_token` | refresh token | 0600 | 静默续期；logout 时吊销并删除 |

> keyring `fileStore`（`store_other.go` 既有 `!windows` 后端）经新构造器 `OpenAt(dir)` 指向 `keys/`。

## 2. `state.Record` 扩展（SchemaVersion 2→3）

| 字段 | 类型 | 变更 | 说明 |
|---|---|---|---|
| `SchemaVersion` | int | 2→3 | loader 接受 ≤3；v2 记录新字段取零值 |
| `NodeID` | int64 | **新增** | 本机节点 ID；onboard 写入；logout/032 按 ID 直查。Windows 端不读该字段（行为不变），其 onboard 路径同步开始写入 |
| 其余字段 | — | 不变 | ServerURL/NodeName/IP/ServerPublicKey/Endpoint/Network/PinnedCertSHA256/FirewallAllowVPN |

不变量（019 语义，路由器版）：**onboard 成功 ⟺ state.json + device_private + refresh_token 三者同时存在且相互一致**；任何失败路径三者同清。

## 3. 协议触点（复用，仅一处扩展）

- `apiclient.RegisterNodePlatform(name, pubKey, platform)`：新增方法，body 带 `platform`（030 服务端已支持）；现有 `RegisterNode` 委托传空（Windows 零改动）。
- `onboard.Provisioner` 增 `Platform string`：空=不传（旧行为）；路由器固定传 `"openwrt"`。
- 其余 API（login/refresh/logout/zones 族/DeleteNode/Me/ListNodes/Server）原样复用。

## 4. 运行态（非持久）

### engine（内核隧道引擎）

| 配置项 | 来源 |
|---|---|
| 接口名 | 固定 `lanweave0`（冲突时启动报错，FR 边界） |
| 本机地址 | `state.IP`（/32）+ VPN 网段路由（`state.Network`） |
| server peer | `state.ServerPublicKey` / `state.Endpoint` / AllowedIPs=`state.Network` / keepalive 25s |
| 私钥 | keyring `device_private` |

### daemon 健康循环（028 对齐）

| 参数 | 值 | 来源 |
|---|---|---|
| 检查周期 | 15s | 028 同款 |
| 陈旧阈值 | 240s | 028 同款 |
| 重试 | 每 15s 到底、无退避、不退出进程 | 028 同款 |

状态机：`启动 → 建隧道 →（握手新鲜 ⟲）→ 陈旧 → 拆建重连 →（失败 ⟲15s）→ 恢复`；SIGTERM → 优雅拆接口退出。

## 5. CLI 与 daemon 的并发约束（FR-014）

- state 写入一律「临时文件 + rename」原子替换（state 包现状已满足）。
- 写前重读合并（最后写入获胜的字段级覆盖不可接受的字段——本切片唯一双写方是 TOFU 指纹与 NodeID，均为 onboard/trust 单点写，实际冲突面为零；登记约束防 033 引入轮询写时回归）。

## 6. 不变量汇总

1. onboard 三件套（state/私钥/RT）同生共死（任何失败路径零半成品）。
2. 隧道运行 ⟺ daemon 进程存活（CLI 不建不拆接口；status 只读）。
3. 日志与命令输出永不出现密码/私钥/令牌明文（宪法 Security，测试断言）。
4. 服务端零行为变化；Windows 客户端除「onboard 开始写 NodeID、apiclient 新增方法」外零行为变化（现有测试全绿钉死）。
