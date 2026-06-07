# lanweave

**自托管、邀请制的 Mesh VPN:Go 服务端/中继 + Windows 桌面客户端,基于 WireGuard 隧道,用 nftables
强制 zone 隔离。**

> English: [../README.md](../README.md) · 完整编译/安装/运行指南:[GUIDE.zh.md](GUIDE.zh.md)

---

## lanweave 是什么

lanweave 让一小群设备组成私有 overlay 网络。运维跑一个服务端(同时是 WireGuard 中继);用户凭**邀请码**
加入、注册设备(node),分配到 `100.127.0.0/16` 网段。只有处于同一 **zone**(带密码的分组)的设备才能
互通,由服务端用 nftables 强制。服务端的 SQLite 是唯一真相来源,WireGuard peer 与防火墙规则都从它重建。

## 功能

- 邀请制注册、JWT 会话(argon2id 密码哈希)
- 按 node 分配 IP(IPAM),网段 `100.127.0.0/16`
- WireGuard 数据平面(服务端接口 + 每 node 一个 peer)
- Zone:同 zone 互通,跨 zone 丢弃(nftables)
- Zone owner 控制:改密、踢人、删 zone
- 基于 WireGuard 握手的 node 在线状态
- 级联删除:删用户即清掉其 node、IP、peer 与 zone 成员关系
- Windows 桌面客户端(Fyne):首次向导、连接/断开、node/zone 管理、自动 UAC 提权
- 打包:Debian `.deb`(systemd,最小权限 `CAP_NET_ADMIN`)与 NSIS Windows 安装器
- CI/CD:GitHub Actions 在每个 `v*` tag 上构建并生成 Release 草稿

## 架构

客户端通过 WireGuard(UDP)连服务端;所有 overlay 流量经服务端中继,由 nftables 套用 zone 规则。Go
服务端掌管 SQLite(状态)、WireGuard 接口与 nftables 表;后两者随时可从数据库重建。服务端在 Linux 上以
root + 收窄的 `CAP_NET_ADMIN` 运行;v1 客户端仅 Windows。

## 快速开始

- **服务端(Debian/Ubuntu)**:装 Release 里的 `.deb`(`sudo dpkg -i lanweave_<ver>_amd64.deb`)或
  `make deb` 自构建。首次安装自动配置并启动 `lanweaved.service`。
- **Windows 客户端**:运行 Release 里的安装器(`lanweave-client-<ver>-setup.exe`),同意 UAC,按首次
  向导走(服务器地址 → 邀请码 → 设备名)。
- 在服务端用 `lanweavectl`(随 `.deb` 安装)管理:`lanweavectl invite` 生成邀请码;
  `lanweavectl user list` / `lanweavectl user del <用户名>` 管理用户。
- 完整细节(工具链、端口、加固、排错)见 **[GUIDE.zh.md](GUIDE.zh.md)**。

## 文档

| 文档 | 说明 |
|------|------|
| [GUIDE.zh.md](GUIDE.zh.md) | 编译·安装·运行指南(中文) |
| [GUIDE.en.md](GUIDE.en.md) | Build, install & run guide(English) |
| [../DESIGN.md](../DESIGN.md) | v1 冻结设计(中文) |
| [ROADMAP.md](ROADMAP.md) | 切片清单与状态 |

## 状态

v1 设计已冻结。14 个切片(从服务端地基到 CI/CD)均已实现;Windows GUI/提权与发布流水线为手动验收
(各 feature 的 spec 内有记录)。默认端口:API `tcp/8443`、WireGuard `udp/51820`。

## 安全

请部署在你自己掌控的服务器上。发布产物**未签名**(用每个 Release 附带的 `SHA256SUMS` 校验;Windows 会
有 SmartScreen 提示)。`.deb` 的 postinstall 会**关闭 ufw**、默认用自签 TLS 证书——上生产前都要复核。
项目级接受风险见 [../DESIGN.md](../DESIGN.md) §11。

## 许可

**GPLv3。** `LICENSE` 文件在首次推送到 GitHub 后通过网页端添加。
