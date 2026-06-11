# Contract: `lanweave-routerd` CLI 子命令（031）

> 单二进制。全局 flag：`--data-dir`（默认 `/etc/lanweave`）。退出码：0 成功；1 一般失败；
> 各命令失败输出单行 `error: <人类可读原因>` 到 stderr。输出永不含密码/私钥/令牌。
> TLS：默认系统 CA；撞 `ErrUntrustedCert` 时打印指纹并指引 `trust` 命令（非交互场景可用
> `--pin <sha256>` 预置）；`--insecure` 为不持久化的逃生口（018 语义）。

## onboard 族

| 命令 | 行为 | 关键 flag |
|---|---|---|
| `setup --server <url>` | 写入服务器地址（state 初始化） | `--pin <sha256>` 可同时预置 TOFU 指纹 |
| `login --username <u>` | 密码自 stdin 读取；换取 access+refresh token 落盘 | `--password-stdin`（默认且唯一方式） |
| `register-account --username <u> --invite <code>` | 邀请码注册新账号并登录 | 密码自 stdin |
| `register --name <node>` | 生成密钥对、以 platform=openwrt 注册本机节点；写 NodeID/IP/server 信息 | — |
| `trust <sha256-fingerprint>` | 显式钉扎服务器证书指纹（TOFU 确认动作） | — |

> `login`/`register-account` + `register` 成功后即「onboard 完成」（三件套一致落盘，FR-002）。
> 已 onboard 再执行 register → 报错提示先 `logout`（不产生第二节点）。

## 隧道族

| 命令 | 行为 |
|---|---|
| `run` | 前台 daemon（procd 调用）：建隧道 → 健康循环（15s/240s 自愈）→ SIGTERM 优雅拆除 |
| `down` | 拆除 wg 接口（procd stop 钩子；幂等） |
| `status` | 输出：`daemon`（running/stopped，按接口存在性）、`tunnel`（connected/disconnected，按最后握手 <3min）、`ip`、`last_handshake`（RFC3339 或 never）、`zones`（逗号分隔）；`key: value` 行格式，字段名稳定（FR-009） |

## zone 族（直连 API，daemon 无需在跑）

| 命令 | 行为 | 错误语义 |
|---|---|---|
| `zone create <name>` | 创建 zone，本机自动入组（015）；密码自 stdin | `zone_name_taken`/配额 → 人类可读 |
| `zone join <name>` | 按名称+密码（stdin）加入 | 「zone 或密码无效」不可枚举 |
| `zone leave <name>` | 本机退出该 zone | 非成员 → 明确报错 |
| `zone list` | 列出所在 zones（名称 + 是否 owner） | — |
| `zone members <name>` | 成员清单：名称 / VPN 地址 / 归属用户 | 非成员 → not found |

## 生命周期

| 命令 | 行为 |
|---|---|
| `logout` | 025 语义：调 API 删本机 node + 吊销 RT（有限重试）→ 成功后清三件套 + 拆接口；不可达 → 非零退出、本地完好、提示 `--force` |
| `logout --force` | 跳过远端清理，仅清本地 + 拆接口，stderr 警告服务端将残留孤儿节点 |

## procd 契约（`packaging/openwrt/lanweave-routerd.init`）

- `start` → `procd_set_param command /usr/bin/lanweave-routerd run`；`respawn` 开（崩溃拉起）
- `stop` → 进程 SIGTERM（daemon 优雅拆接口）；兜底 `down`
- `enable` 后开机自启（START=95，网络就绪之后）

## 回归契约

- 服务端零接口/行为变化；`internal/client` 既有包仅三处加法（apiclient 新方法、onboard Platform 字段、state NodeID 字段 + schema v3），Windows 路径现有测试全绿。
- `unshare -rUn bash -c 'ip link set lo up && go test ./...'` 全绿；`make routerd-cross` 三目标产物生成。
