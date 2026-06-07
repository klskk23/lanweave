# Quickstart & Acceptance: server-http-mode

## 无头自动化（CI 可跑）

```bash
# 配置纯逻辑：三态 TLSEnabled / 证书条件校验 / 明文绑定告警决策（无需特权）
go test ./internal/server/config/...

# 全量回归（真 SQLite/nftables/WireGuard；本特性只改监听传输，须保持全绿）
unshare -rUn sh -c 'ip link set lo up && go test ./...'

# 改动文件 lint（宪法 I）
gofmt -l internal/server/config internal/server/app
go vet ./internal/server/config/... ./internal/server/app/...
staticcheck ./internal/server/config/... ./internal/server/app/...
```

**自动化覆盖映射**
| 项 | 测试 | 层 |
|----|------|----|
| 三态解析（缺省/true→HTTPS；false→明文） | `config_test.go` `TLSEnabled()` | 单元 |
| FR-005 明文模式忽略证书 | `config_test.go` `Validate()` 明文 + 空/坏证书无错 | 单元 |
| FR-004 TLS 模式缺证书硬失败 | `config_test.go` `Validate()` TLS + 缺证书有错 | 单元 |
| FR-006/007 告警决策真值表 | `config_test.go` `WarnPlaintextExposure()` | 单元 |
| US1 明文模式打通受保护调用（SC-001） | `app_test.go` 裸 http 客户端 login→受保护端点 | 集成（`unshare`） |
| US2-1 缺省=HTTPS（SC-002） | `app_test.go`（含既有 `TestRunServesAndShutsDown`） | 集成 |
| US2-2 TLS 缺证书硬失败（SC-003） | `app_test.go` 仿 `TestRunRejectsMissingAdminPassword`（无特权） | 集成（配置失败） |
| US3 明文绑 `0.0.0.0` 不拦截（SC-004） | `app_test.go` `tls=false`+`0.0.0.0:0` 成功 Ready | 集成（`unshare`） |
| SC-005 无回归 | `unshare -rUn … go test ./...` 全绿 | 全量 |

> US3 的「WARN 出现」由 `WarnPlaintextExposure()` 纯函数单元测确定性覆盖；集成层证明「非回环明文绑定不阻断启动」即其端到端要义（不为捕获日志行而给 `app.Options` 加注入 logger）。

## 反向代理手工冒烟（可选，验证真实部署形态）

模拟「反代终止 TLS → 转发明文」端到端（在一台 Linux 上）：

1. 配置服务端 `tls = false`、`listen = "127.0.0.1:8080"`，正常启动（WG/nft 照常 boot）。
2. 直接打明文控制面（绕过反代，确认明文可用）：
   ```bash
   curl -s http://127.0.0.1:8080/api/v1/healthz   # → 200
   ```
3. 起一个终止 TLS 的反代（nginx/Caddy 任一），`proxy_pass http://127.0.0.1:8080;`，对外 `https://`。
4. 客户端（**零改动**）照常连反代的 `https://` 地址，走完向导 / 连隧道 / 管理 zone——行为与服务端 HTTPS 模式无差别。

## 回归 / 安全确认矩阵（启动行为）

| # | 配置 | 期望 |
|---|------|------|
| C1 | 无 `tls` 键（既有配置升级） | HTTPS 监听，日志 `https listening`；明文 curl 失败 |
| C2 | `tls = true` + 证书就绪 | HTTPS 监听（同 C1） |
| C3 | `tls = true` + 证书缺失/坏 | 启动**硬失败**、非 0 退出，不退回明文 |
| C4 | `tls = false` + `listen=127.0.0.1:8080` | 明文监听，日志 `http listening (plaintext)`；**无** WARN |
| C5 | `tls = false` + `listen=0.0.0.0:8080` | 明文监听 + 一条 WARN（须反代、勿暴露公网）；**不**拦截启动 |
| C6 | `tls = false` + 证书缺失 | 正常明文启动（证书被忽略） |

C1–C2、C4–C6 的可启动项可在 `unshare -rUn` 内真 boot 验证；C3 为纯配置校验失败，`go test` 直接断言 `app.Run` 返回错误。

## 回归确认

- 控制面所有 `/api/v1/*` 端点、鉴权、限流在两模式下行为一致（FR-008）。
- 客户端、WireGuard 数据面、nftables 隔离、打包 postinstall 证书生成零改动（FR-009、SC-005）。
- DESIGN.md 控制面措辞 + §11 风险登记、`config.toml.example` 注释已同步（FR-010）。
