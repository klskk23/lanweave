# lanweave — 编译、安装与运行指南

> English version: [GUIDE.en.md](./GUIDE.en.md)。

本指南覆盖三条完整流程:**编译**(服务端 + Windows 客户端)、**安装**、**运行**(含获取邀请码与客户端连接)。

> 约定
> - 服务端:Debian/Ubuntu,以 root + `CAP_NET_ADMIN` 运行(内核 WireGuard + nftables)。
> - 客户端:Windows 10/11 桌面(Fyne GUI + WinTun)。
> - 默认端口:API `tcp/8443`、WireGuard `udp/51820`;VPN 网段 `100.127.0.0/16`(服务端 = `100.127.0.1`)。

---

## 目录

1. [编译](#1-编译)
   - 1.1 服务端
   - 1.2 Windows 客户端
   - 1.3 Windows 安装包(NSIS)
2. [安装](#2-安装)
   - 2.1 服务端 `.deb`
   - 2.2 Windows 客户端
3. [运行](#3-运行)
   - 3.1 服务端服务
   - 3.2 防火墙与端口
   - 3.3 获取邀请码
   - 3.4 客户端首次运行
   - 3.5 连接与验证
   - 3.6 排错
4. [卸载与数据保留](#4-卸载与数据保留)

---

## 1. 编译

> **正式发布是自动构建的。** push 一个 `vX.Y.Z` tag 会触发 GitHub Actions:跑测试、构建 `.deb` +
> Windows 安装器,并生成一个含全部产物与 `SHA256SUMS` 的 GitHub Release 草稿(人工审阅后手动发布)。
> 下面是本地/手动构建的步骤。

### 1.1 服务端

需要 Go 1.26。可只编静态服务端二进制,或直接打 `.deb`(会顺带编二进制)。`make deb` 需要
[`nfpm`](https://github.com/goreleaser/nfpm)。

```sh
make build      # → ./lanweaved
make deb        # → dist/lanweave_<version>_amd64.deb   (需要 nfpm)
```

不用 make 的等价命令:

```sh
CGO_ENABLED=0 go build -ldflags "-X main.version=0.1.0" -o lanweaved ./cmd/lanweaved
```

### 1.2 Windows 客户端

GUI 客户端基于 Fyne,需要 **cgo**,因此必须在 **Windows** 上配 C 工具链编译。二选一安装:

- **MSYS2(推荐)** — 从 <https://www.msys2.org> 安装,打开 **UCRT64** 终端,然后:
  ```bash
  pacman -Syu                                  # 更新,提示时重开终端
  pacman -S mingw-w64-ucrt-x86_64-gcc          # 安装 gcc
  ```
  把 `C:\msys64\ucrt64\bin` 加入 Windows **PATH**。
- **TDM-GCC** — 从 <https://jmeubank.github.io/tdm-gcc/> 安装,选 64 位,勾选自动加入 PATH。

验证:新开终端里 `gcc --version`、`go version` 均可用。

编译客户端:

```bat
set CGO_ENABLED=1
go build -tags gui -ldflags "-H windowsgui -X main.version=0.1.0" -o lanweave-client.exe .\cmd\lanweave-client
```

- `-tags gui` 启用真正的 Fyne UI(不加只会编出 headless 占位桩)。
- `-H windowsgui` 去掉控制台黑窗。

然后把匹配的 **`wintun.dll`**(amd64)放到 `lanweave-client.exe` 同目录:从 <https://www.wintun.net>
下官方 zip,取 `bin\amd64\wintun.dll`。

> **常见坑 — `error obtaining VCS status: exit status 128`**
> Go 会把 git 信息打进二进制。Windows 上常因 git 的「dubious ownership」保护失败。二选一修复:
> ```bat
> git config --global --add safe.directory "C:/path/to/lanweave"   :: 根治
> ```
> 或给 `go build` 加 `-buildvcs=false`(版本号已用 `-ldflags` 注入,不受影响)。

### 1.3 Windows 安装包(NSIS)

用 [NSIS](https://nsis.sourceforge.io) 打包。三个文件放**同一目录**(脚本按相对名引用 exe/dll):

```
packaging\windows\
├── lanweave-client.nsi
├── lanweave-client.exe      # 来自 1.2
└── wintun.dll               # amd64
```

```bat
cd packaging\windows
makensis lanweave-client.nsi      :: → lanweave-client-setup.exe
```

- 安装器请求管理员权限,仅用于装 WinTun 驱动 / 写入 Program Files。
- **装好的 app 在运行时自动提权(UAC)** 来创建网卡 —— 不嵌入 manifest(见
  `internal/client/winelevate`)。
- `.nsi` 必须是**纯 ASCII**,否则报 `Bad text encoding`。

---

## 2. 安装

### 2.1 服务端 `.deb`

```sh
sudo dpkg -i dist/lanweave_<version>_amd64.deb
# 或自动拉取 openssl 依赖:
sudo apt install ./dist/lanweave_<version>_amd64.deb
```

**首次**安装时,postinstall 让服务开箱即用:

- 创建 `/var/lib/lanweave/`(仅 root)存数据库与服务端密钥;
- 若无 `/etc/lanweave/config.toml`,从示例生成(随机 admin 密码 + JWT 密钥,`0600`);
- 用 `openssl` 生成**自签** `cert.pem` / `key.pem`;
- 把生成的 admin 密码写入 **`/etc/lanweave/initial-admin-password`**(`0600`)—— **不**打印到终端/日志;
- 启用并启动 `lanweaved.service`。

> ⚠️ **防火墙提示**:postinstall 还会执行 **`ufw disable`** 关闭宿主机防火墙。若你依赖 ufw,请重新
> 启用并放行 §3.2 的端口。

上生产前加固(必须):

1. 读取初始 admin 密码:
   ```sh
   sudo cat /etc/lanweave/initial-admin-password
   ```
2. 用受信证书替换自签 `/etc/lanweave/cert.pem` / `key.pem`(如 Let's Encrypt),或在每台客户端预装自签 CA。
3. 通过客户端改 admin 密码,然后删除该文件:
   ```sh
   sudo rm -f /etc/lanweave/initial-admin-password
   ```

升级:`sudo dpkg -i <newer>.deb` —— 保留你的 `config.toml` 与 `/var/lib/lanweave/` 数据(示例配置不覆盖现用配置)。

### 2.2 Windows 客户端

运行 `lanweave-client-setup.exe`,**同意 UAC**(装驱动、写 Program Files 需要)。它把
`lanweave-client.exe` + `wintun.dll` 装到 `C:\Program Files\lanweave\`,并建开始菜单/桌面快捷方式。

---

## 3. 运行

### 3.1 服务端服务

```sh
systemctl status lanweaved      # → active (running)
journalctl -u lanweaved -f      # 跟踪日志
```

服务以 root + `CapabilityBoundingSet=CAP_NET_ADMIN` 运行,失败自动重启,日志进 journal。启动时还会
开启宿主机 IPv4 转发(`net.ipv4.ip_forward=1`),你**无需**手动设置。

### 3.2 防火墙与端口

若启用了防火墙(`.deb` 会关 ufw,见 §2.1),放行:

| 端口        | 用途 |
|-------------|------|
| `tcp/8443`  | HTTPS API(登录、注册、node/zone 管理) |
| `udp/51820` | WireGuard 隧道 |

云安全组(AWS/GCP 等)也要放行同样端口。

### 3.3 获取邀请码

邀请码由**管理员**通过 API 签发,客户端用它注册。
1. 使用脚本 `/usr/local/bin/lanweave-invite-codegen.sh`
2. 手动签发

  ```sh
  # 1) 取 admin 密码(首次安装时生成)
  sudo cat /etc/lanweave/initial-admin-password

  # 2) 登录换 JWT(自签证书 → 本机引导用 -k)
  TOKEN=$(curl -sk https://localhost:8443/api/v1/login \
    -d '{"username":"admin","password":"<密码>"}' | jq -r .token)

  # 3) 签发一次性邀请码
  curl -sk -X POST https://localhost:8443/api/v1/admin/invites \
    -H "Authorization: Bearer $TOKEN" | jq -r .code
  # `-k` 跳过 TLS 校验,仅限本机引导;外部调用改用 `--cacert /etc/lanweave/cert.pem` 并用证书主机名访问。
  ```
3. 邀请码**一次性**;`GET /api/v1/admin/invites` 查状态。


### 3.4 客户端首次运行

正常方式启动(双击快捷方式)。Windows 会弹**一次 UAC**——app 会自动提权(建网卡需管理员),你**无需**
右键“以管理员身份运行”。同意后进入首次向导:

1. 服务器地址 —— 例如 `https://vpn.example.com:8443`
2. 登录,或用 §3.3 的**邀请码**注册新账号
3. 设备(node)名 → app 生成 WireGuard 密钥对并注册 node

私钥存入 Windows 凭据管理器;node IP / 服务器信息写入 `%LOCALAPPDATA%\lanweave\state.json`。

> 若拒绝 UAC,app 会提示一句并关闭(无管理员权限无法建网卡)。

### 3.5 连接与验证

1. 点面板里的**连接**。
2. `ipconfig` 出现 `100.127.x.y` 网卡。
3. `ping 100.127.0.1` 通服务器。
4. 要互通其它设备,在面板创建/加入 **zone**(名 + 密码);同 zone 才能互通。

### 3.6 排错

| 现象 | 原因与解决 |
|------|------------|
| “could not set up the network adapter” | 未提权或缺 `wintun.dll`。客户端会自动提权(同意 UAC);确认 `wintun.dll`(amd64)在 exe 同目录。 |
| TLS / 证书不受信 | 自签证书:在客户端信任 CA,或(高级)`--insecure` 启动客户端。 |
| 连不上服务器 | 防火墙/安全组拦截;放行 `tcp/8443` + `udp/51820`(§3.2)。 |
| 已连接但 ping 不通其它 node | 两台不在同一 zone。 |

---

## 4. 卸载与数据保留

### 服务端

| 命令 | 效果 |
|------|------|
| `sudo apt remove lanweave`(或 `dpkg -r`) | 停服并禁用、删程序文件;**保留** `/var/lib/lanweave` + `/etc/lanweave`,重装可续。 |
| `sudo apt purge lanweave`(或 `dpkg -P`) | 连 `/var/lib/lanweave` + `/etc/lanweave` 一并删除。 |

自行备份 `/var/lib/lanweave/db.sqlite`(承载全部状态):`sqlite3 db.sqlite .backup backup.sqlite`。

### Windows 客户端

从**程序和功能**卸载(或 `C:\Program Files\lanweave\uninstall.exe`)。它删程序文件但**保留**设备身份:
**凭据管理器**里的密钥 + 会话令牌,以及 `%LOCALAPPDATA%\lanweave\state.json`。

要彻底清除身份,卸载后删除 `%LOCALAPPDATA%\lanweave\` 并移除凭据管理器中的 lanweave 条目。

---

## 实机验收

- **服务端**:干净 Debian 上 `dpkg -i` → `systemctl status lanweaved` 为 active;`kill -9` 进程会
  自动重启;`apt remove` 保留数据、`apt purge` 删除数据。
- **Windows**:installer 装好 app + 驱动 + 快捷方式并提权;启动 app 自动提权(一次 UAC)、进入首次
  设置、Connect 建出 `100.127.x.y` 网卡;卸载删程序文件并保留用户 secrets/state。
