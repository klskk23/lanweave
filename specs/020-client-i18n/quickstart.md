# Quickstart & Acceptance: client-i18n

## 无头自动化(CI 可跑,无 gui tag)

`internal/client/i18n` 为纯逻辑,直接验证:

```bash
# 语言解析 + 目录键集一致 + T 回退
go test ./internal/client/i18n/...

# 全量回归(本期不触 SQLite/nftables/WireGuard,应保持全绿)
unshare -rUn sh -c 'ip link set lo up && go test ./...'

# 改动文件 lint(宪法 I)
gofmt -l internal/client/i18n internal/client/ui cmd/lanweave-client
go vet ./internal/client/i18n/...
```

**自动化覆盖映射**
| 项 | 测试 |
|----|------|
| US1 系统语言解析(zh→中文,其余→英文) | `i18n_test.go` 表驱动 `Resolve("", sysLocale)` |
| US2 偏好压过系统 | `Resolve("zh-Hans","en-US")==ZhCN`、`Resolve("en","zh-Hans-CN")==En` |
| US3 跟随系统(空 pref) | `Resolve("", …)` 回到系统 locale 分支 |
| FR-008 键集一致 + 回退 | 双向差集为空断言;`T(缺失键)` 回退英文/返回 key |
| SC-005 无回归 | `unshare -rUn … go test ./...` 全绿 |

> 偏好三态写入/复位、选择器交互、实际界面语言为 GUI 行为,经下方 Windows 手工矩阵覆盖(同 016/018 惯例)。

## Windows 手工验收矩阵(gui 构建)

构建:`go build -tags gui ./cmd/lanweave-client`(Windows,需 mesa `opengl32.dll`,见项目备忘)。

| # | 前置 | 操作 | 期望 |
|---|------|------|------|
| M1 | 中文 Windows、无 `ui.language` 偏好(全新) | 启动客户端进入向导 | 向导全中文(标题/按钮/字段/错误提示) |
| M2 | 英文 Windows、无偏好 | 启动 | 界面全英文 |
| M3 | 任一语言、已进面板 | 触发一个错误(如断网点连接) | 错误提示为当前界面语言的人类可读句子,非英文 Go error 链 |
| M4 | 英文界面、向导顶部选择器 | 选「中文」 | 弹「下次启动后生效」;当前界面**不**立即变 |
| M5 | 承 M4 | 完全退出并重启 | 界面变简体中文(无视系统为英文) |
| M6 | 承 M5 | 再次完全退出并重启 | 仍为中文(偏好持久) |
| M7 | 面板页脚选择器 | 在面板处切换语言 | 行为与向导处一致(同提示、重启后生效) |
| M8 | 已手动选「中文」(系统英文) | 选「跟随系统」并重启 | 界面回到英文(系统 locale) |
| M9 | 全新安装 | 打开任一选择器 | 当前选中项为「跟随系统」 |
| M10 | 任一语言 | 通览向导 3 步 + 面板(节点/区域 tab、创建/加入/成员/改密/删除/踢人确认框、防火墙开关、退出登录确认) | 无任何残留英文硬编码、无可见原始键名/空白 |

全部通过 = 三个用户故事(US1/US2/US3)在真机达成。

## 回归确认

- 引入 i18n 不破坏既有无头测试(onboard/panel/state/apiclient 等)。
- 服务端、tunnel、firewall、keyring 行为零改动(仅 ui 层文案 + 新 i18n 包 + main.go 一行装载)。
