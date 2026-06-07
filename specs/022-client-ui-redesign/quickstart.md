# Quickstart & 验收矩阵: client-ui-redesign

本切片的验收分两层:**自动化**(headless 控件测试 + 纯逻辑 + `unshare -rUn go test ./...` 全绿)与 **GUI 人工矩阵**(Mesa OpenGL VM 上肉眼逐条过 `UI-DESIGN.md §8`)。沿用 017/018/020 已登记的宪法 II「纯 GUI 呈现走人工」分工。

---

## A. 构建与运行

```sh
# 无头逻辑 + 控件测试(CI 可跑,无需 OpenGL)
go test ./internal/client/...

# 宪法 II 全量门禁(服务端逻辑本期不动,须保持全绿)
unshare -rUn go test ./...

# 构建 GUI 客户端(gui tag)
go build -tags gui ./cmd/lanweave-client

# Mesa OpenGL VM(Hyper-V Win11):需放置 mesa-dist-win 的 opengl32.dll 于 exe 同目录后再启动
lanweave-client.exe
```

---

## B. 自动化验收(每 user story ≥1,宪法 II)

| Story | 测试(headless / 纯逻辑) | 断言 |
|-------|--------------------------|------|
| US1 | `fyne/test` 构建 Hero 片段 | 未连接→唯一「立即连接」按钮、无第二按钮;`tunnel.State()==Connected`→按钮文案变「断开连接」;Switch `On` 反映并回写 `FirewallAllowed()`(fake controller) |
| US1 | 主题 | `NewTheme().Color(Background,VariantLight)` 仍返回深色 surfaceBase(强制深色) |
| US2 | overflow 菜单构建 | insecure=true→含红「证书未验证」项;`PinnedCertSHA256!=""`→含中性「已在本机信任」项;system-CA→两者皆无;含语言子菜单 + 退出项 |
| US3 | 节点行渲染 | 本机行含「本机」chip + 高亮色 + 不可点;离线行文案含「N 分钟前离线」且用 textTertiary;在线/离线状态点颜色正确 |
| US3 | 区域行 | `Tapped` 触发打开区域详情(可断言回调/内容);owner 区域详情含改密/踢人/删除,非 owner 只读 |
| US4 | `Transfer()` 解析(`tunnel_test.go`,纯逻辑) | fake engine 喂含 `rx_bytes=`/`tx_bytes=` 的 UAPI 文本 → 返回正确求和;未连接→`(0,0,nil)` |
| US4 | 字节格式化(纯函数) | 0/1023/1024/1.5MB/GB 等边界 → 期望可读串(B/KB/MB/GB) |
| US5 | wizard 渲染冒烟 | 套主题后四步可渲染、Back/Cancel/Next 回调不变(流程逻辑零改动) |

---

## C. GUI 人工矩阵(Mesa VM,对照 `UI-DESIGN.md §8`)

逐条肉眼核对,全部满足才算 SC-001 通过:

- [ ] App Bar 左对齐 logo + 「lanweave」,**非居中大标题**
- [ ] 顶部**没有**常驻「退出登录」按钮(退出在 ⋮ overflow 内、红色置底)
- [ ] 连接/断开是**单一主按钮**,非两个并排
- [ ] 状态用圆点 + 颜色 + 简短文字,**非 `[离线]` 括号文本**
- [ ] 节点列表每行有 avatar + 状态点
- [ ] 本机一行有「本机」chip + 浅色背景高亮
- [ ] 离线节点文字为 textTertiary(最浅),含「N 分钟前离线」
- [ ] 「允许 VPN 入站访问」在 Hero 卡片内部底部,**非单独一行**;为 Switch **非 checkbox**
- [ ] Tab 名「节点」/「区域」,**非「我的节点/我的区域」**;选中有 2px brandCyan 指示条;带计数
- [ ] **无**渐变 / 阴影 / blur / glow
- [ ] 主操作按钮 pill 圆角(999),非 8px 小圆角
- [ ] IP / CIDR 用 monospace 显示

补充人工核对(超出 §8 但属本切片范围):

- [ ] ⋮ overflow:语言三项(跟随系统/中文/English)切换后提示重启生效;insecure 会话见红「证书未验证」项、自签 pin 见中性「已在本机信任」项
- [ ] 区域行整行点击 → 打开区域详情;owner 可改密/踢人/删除,非 owner 只读 + 退出
- [ ] 右下「+」FAB → 创建/加入二选一,分别进既有流程并成功
- [ ] 连接后 Hero 出现 ↑/↓ 流量并随时间增长;断开后流量消失并复位
- [ ] Wizard 外观(深色/字号/pill/卡片/顶部语言选择器)与主面板一致;四步走通注册/登录/建节点进主面板,行为同改版前
- [ ] 中/英两套 + 跟随系统,重启生效(沿用 020)

---

## D. 行为零回归核对(SC-004)

改版后下列行为须与改版前完全一致(自动化 + 人工抽查):

- [ ] 连接/断开隧道(`ipconfig` 网卡出现/消失);Connecting 期间按钮禁用
- [ ] 防火墙开关:ON+连接→`netsh ... show rule name=lanweave-vpn-inbound` 存在;OFF/断开/退出→消失;开关状态重启保持
- [ ] 退出登录:确认弹窗点名设备+服务器 → 断连 + 注销本机 node + 清凭据 + 回 wizard 首步
- [ ] 区域:创建(自动入组)/加入他人/退出/(owner)改密·踢人·删除
- [ ] TOFU:首连自签弹信任 → 接受后静默;证书变更弹重警告;`--insecure` 仍 CLI-only
- [ ] `unshare -rUn go test ./...` 全绿(服务端 SQLite/nftables/WireGuard 逻辑未动)

---

## E. DESIGN.md 同步核对(宪法 DESIGN.md authority)

- [ ] DESIGN.md §9.4 主面板重写为新布局(App Bar+overflow / Hero+Switch / 节点·区域 tab / 区域详情 / FAB / 流量)
- [ ] DESIGN.md §282/§285 信任指示改记为 App Bar overflow 菜单项(insecure 红项 / 自签中性项),不再「常驻警示」;注明 022 已接受 UX 取舍
