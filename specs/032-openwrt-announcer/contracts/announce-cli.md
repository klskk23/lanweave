# Contract: `lanweave-routerd announce` 命令族（032）

> 031 CLI 形态延续：非交互、退出码 0/1、stderr 单行 `error: …`、输出零机密。
> 全局 flag（--data-dir/--iface/--insecure）继续适用。需已 onboard（state.NodeID 就位）。

## `announce add <subnet> --zone <name>`

把本机背后的真实子网宣告进 zone。

- 流程：远端创建（030 全套校验）→ 本地合成段冲突检测（FR-008）→ 本地翻译规则全量重建 → 输出映射。
- **成功输出**（stdout，字段稳定）：
  `announced 192.168.50.0/24 -> 100.100.1.0/24 (zone homelab, id 7)`
- 同子网再 add 到第二个 zone：复用同一合成段（030 语义），输出同上（id 相同）。
- 子网非本机直连时附 stderr 提示（不阻拦）：`note: ensure this router can reach 10.8.0.0/24`。
- **失败**（全部非零退出 + 人类可读，远端/本地零半成品）：

| 场景 | 信息要点 |
|---|---|
| 030 六类拒绝 | 对应 typed error 文案（平台/停用/子网非法/自身重叠/配额/池耗尽） |
| 非 zone 成员 / zone 不存在 | not found 族（不可枚举） |
| 合成段撞本机网段（FR-008） | 指明冲突接口与网段；已自动撤回远端挂接 |
| 本地规则下发失败（FR-005） | 报错 + 已补偿撤回；补偿失败时双重告警 |

## `announce remove <subnet> --zone <name>`

撤回该子网在指定 zone 的挂接（id 由清单自动解析，用户不接触 id）。

- 成功：`withdrawn 192.168.50.0/24 from zone homelab`；最后挂接消失时本地规则随重建移除。
- 该子网未宣告到该 zone → 非零退出 + not found。

## `announce list`

聚合本机全部宣告（跨 zone 去重展示）。

```
SUBNET             SYNTHETIC          ZONES           RULES
192.168.50.0/24    100.100.1.0/24     homelab,office  ok
10.8.0.0/24        100.100.2.0/24     homelab         pending
```

- `RULES`：`ok`=本地规则与真源一致；`pending`=对账中/上次对账失败（FR-011）。
- 未 onboard / 无宣告 → 友好空态输出，退出码 0。

## 对账行为契约（无命令，daemon 附属）

- `run` 启动即对账一次；运行期每 60s 一次；`add/remove` 成功后立即本地重建。
- 服务端被第三方移除的宣告 ≤60s 内本地规则清除（SC-002）。
- API 不可达：跳过本轮、记日志、规则保持现状（服务端数据面已收口，残留无害）。

## logout 扩展契约（031 命令行为追加）

- `logout`（含 `--force`）MUST 额外删除本机翻译规则表（与隧道拆除同场）。

## 回归契约

- 服务端零变化；Windows 客户端零变化；031 全部命令行为不回归；apiclient 三个新方法为纯加法。
- `unshare -rUn bash -c 'ip link set lo up && go test ./...'` 全绿。
