---
description: "Task list for feature 021 server-http-mode"
---

# Tasks: server-http-mode

**Input**: Design documents from `specs/021-server-http-mode/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/server-transport.md, quickstart.md

**Tests**: 本特性跨进程边界（控制面监听器），宪法 II（NON-NEGOTIABLE）要求单元 + 集成 + 每个用户故事 ≥1 端到端验收。下列测试任务为强制。

**Organization**: 按用户故事分组（US1/US2/US3），每组可独立实现与测试。共享原语 `TLSEnabled()` 下沉到 Foundational，使各故事仅依赖 Foundational（消除跨故事隐藏依赖，见 analyze F1）。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完依赖）
- **[Story]**: US1 / US2 / US3
- 路径为仓库根相对路径

## Path Conventions

- 单 Go module 的 server 子树：`internal/server/config/`、`internal/server/app/`、根 `config.toml.example`、`DESIGN.md`。

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 确认基线、无新依赖

- [X] T001 确认起点基线：`unshare -rUn sh -c 'ip link set lo up && go test ./internal/server/...'` 全绿，并确认无需新增第三方依赖（`*bool` 三态由既有 `github.com/pelletier/go-toml/v2` 解码，见 research.md R1）。

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 所有故事共享的传输开关原语（字段 + `TLSEnabled()`）——US1/US2/US3 的实现与测试均直接依赖它

**⚠️ CRITICAL**: 本阶段完成前，任何用户故事不能开始

- [X] T002 在 `internal/server/config/config_test.go` 增 `TLSEnabled()` 三态单元测试：解码无 `tls` 键→true、`tls=true`→true、`tls=false`→false。引用尚未实现的 `TLSEnabled()`，应编译失败=红（参照 020 惯例）。
- [X] T003 在 `internal/server/config/config.go` 的 `ServerConfig` 增字段 `TLS *bool` `toml:"tls"`（三态指针），并实现 nil 安全方法 `func (s ServerConfig) TLSEnabled() bool { return s.TLS == nil || *s.TLS }`。运行 T002 转绿；既有测试保持通过。

**Checkpoint**: 传输开关原语就位 → 各用户故事可独立开始

---

## Phase 3: User Story 1 - 在反代后以明文 HTTP 运行服务端 (Priority: P1) 🎯 MVP

**Goal**: `tls = false` 时服务端监听明文 HTTP，受保护控制面调用照常工作，不要求证书。

**Independent Test**: `tls=false` 启动，用裸 `http.Client` 完成 login 并携 token 调受保护端点成功（quickstart C4 / SC-001）。

**Depends on**: Foundational（T003 的 `TLSEnabled()`）。与 US2 无逻辑耦合。

### Tests for User Story 1 (REQUIRED per constitution Principle II) ⚠️

> 先写、先红：T005 实现 app.go 分叉前，app.go 仍恒走 TLS，裸 http 连接 TLS 监听器失败=红。

- [X] T004 [US1] 在 `internal/server/app/app_test.go` 增集成验收 `TestRunServesPlaintextHTTP`（复用 `writeConfig`/`newCerts`/`Ready` 骨架，配置写 `tls = false`、`listen=127.0.0.1:0`，证书照写但应被忽略）：真启动后用**裸 `http.Client`**（无 TLS）打 `http://addr` 完成 admin login → 携 token 调受保护端点（如 `GET /api/v1/nodes`）返回成功。`testutil.RequireNetAdmin` + `unshare -rUn`。

### Implementation for User Story 1

- [X] T005 [US1] 在 `internal/server/app/app.go` 按 `cfg.Server.TLSEnabled()` 分叉监听器（见 research.md R3）：TLS 分支保留现有证书加载 + `tls.NewListener` + `https listening` 日志；明文分支**跳过**证书加载、不设 `srv.TLSConfig`、`srv.Serve` 裸 `net.Listener`、日志 `http listening (plaintext)`。`Ready` 回调位置不变。运行 T004 转绿。

**Checkpoint**: US1 独立可用——明文模式端到端打通，即 MVP。

---

## Phase 4: User Story 2 - 默认与既有配置始终保持 HTTPS（绝不静默降级） (Priority: P1)

**Goal**: 缺省/`tls=true` 仍 HTTPS；TLS 模式缺/坏证书硬失败；明文模式忽略证书（含缺失）。

**Independent Test**: 不写 `tls` 的配置启动→HTTPS（quickstart C1/SC-002）；`tls=true`+缺证书→`app.Run` 返回错误（C3/SC-003）；`tls=false`+缺证书→`Validate` 不报证书错（FR-005）。

**Depends on**: Foundational（T003 的 `TLSEnabled()`）。**不依赖 US1**（改的是 `config.go` 的 `Validate()`，与 app.go 监听分叉正交）。注：与 US1/US3 同改 `config_test.go`/`app_test.go`，存在文件冲突串行（非逻辑依赖）。

### Tests for User Story 2 (REQUIRED per constitution Principle II) ⚠️

- [X] T006 [US2] 在 `internal/server/config/config_test.go` 增 `Validate()` 证书条件化单元测试：明文模式（`tls=false`）下 `tls_cert`/`tls_key` 为空或不可读 → **无**证书相关错误（FR-005）；TLS 模式（缺省 与 `tls=true` 两例）下缺证书 → **有**证书错误（FR-004）。当前 `Validate` 无条件校验，明文+空证书断言应失败=红。
- [X] T007 [P] [US2] 在 `internal/server/app/app_test.go` 增两个验收：(a) `TestRunDefaultConfigIsHTTPS`——不写 `tls` 键，真启动后 TLS 客户端打 `/healthz` 200 且裸 http 打同端口失败（锁定缺省=HTTPS，SC-002；与既有 `TestRunServesAndShutsDown` 互补）；(b) `TestRunTLSMissingCertHardFails`——`tls=true`（或缺省）+ 指向不存在的证书路径，`app.Run` 返回非 nil 错误（纯配置校验失败，发生在 `cfg.Validate()`，**先于**数据面 setup，**无需特权**，仿 `TestRunRejectsMissingAdminPassword`，SC-003）。

### Implementation for User Story 2

- [X] T008 [US2] 在 `internal/server/config/config.go` 的 `Validate()` 把现有证书三步（`requireReadable` ×2 + `tls.LoadX509KeyPair`，config.go:149-155）包进 `if c.Server.TLSEnabled() { ... }`：TLS 模式照旧硬校验（FR-004），明文模式整段跳过（FR-005）。`server.listen` 等其余校验保持无条件不变。运行 T006/T007 转绿。

**Checkpoint**: US1+US2 均独立通过——明文可用 且 安全默认/硬失败被锁定。

---

## Phase 5: User Story 3 - 明文绑定非回环地址时的启动告警 (Priority: P2)

**Goal**: 明文 + 非回环（含 `0.0.0.0`）启动打一条 WARN，但不拦截；明文+回环 与 TLS 模式无此告警。

**Independent Test**: `tls=false`+`0.0.0.0:0` 启动成功 Ready（非阻断，quickstart C5/SC-004）；告警决策真值由纯函数单元测覆盖。

**Depends on**: Foundational（T003 的 `TLSEnabled()`）；**T012 额外依赖 US1 的 T005**（在其所建明文分支内加告警）。

### Tests for User Story 3 (REQUIRED per constitution Principle II) ⚠️

- [X] T009 [US3] 在 `internal/server/config/config_test.go` 增 `WarnPlaintextExposure()` 真值表单元测试（表驱动，覆盖 data-model.md 告警表全部分支）：明文+`127.0.0.1`/`::1`/`localhost`→false；明文+`0.0.0.0`/`::`/`:8080`(空 host)/真实 IP/主机名→true；TLS 模式任意地址→false。引用未实现方法，应编译失败=红。
- [X] T010 [P] [US3] 在 `internal/server/app/app_test.go` 增集成验收 `TestRunPlaintextNonLoopbackStarts`：`tls=false`+`listen=0.0.0.0:0` 真启动，断言成功收到 `Ready`（证明非回环明文绑定**不拦截**启动，FR-006「不拦」）。`unshare -rUn`。

### Implementation for User Story 3

- [X] T011 [US3] 在 `internal/server/config/config.go` 实现纯函数 `listenIsLoopback()`（`net.SplitHostPort`+`net.ParseIP`+`IP.IsLoopback`，`localhost`→true，空 host/主机名/解析失败→非回环，见 research.md R4）与 `func (s ServerConfig) WarnPlaintextExposure() bool { return !s.TLSEnabled() && !s.listenIsLoopback() }`。运行 T009 转绿。
- [X] T012 [US3] 在 `internal/server/app/app.go` 的明文分支（T005 所建）加：`if cfg.Server.WarnPlaintextExposure() { log.Warn("plaintext HTTP on a non-loopback address; terminate TLS at a reverse proxy and do not expose this listener publicly", "addr", ln.Addr().String()) }`，置于 `http listening (plaintext)` 日志之前，不影响 `Serve`。运行 T010 仍绿（非阻断）。

**Checkpoint**: 三个用户故事均独立功能完整。

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: FR-010 文档同步（宪法强制，同 PR）+ 全量回归

- [X] T013 [P] 在 `DESIGN.md` 落 FR-010：第 53 行「控制面：HTTPS REST」→「HTTPS REST（或经 TLS 终止反代后的明文 HTTP）」；第 221 行「全部 HTTPS」措辞放宽为「HTTPS，或 TLS 终止反代后的明文 HTTP」；§11 风险登记表（line 368 起）新增一行接受项：「服务端明文 HTTP 监听（`tls=false`）| 仅显式开启才明文；须置于 TLS 终止反代之后；非回环绑定启动告警；勿暴露明文监听公网；缺省/`tls=true` 仍 HTTPS 且缺证书硬失败」。
- [X] T014 [P] 在 `config.toml.example`：第 7 行注释「HTTPS only; there is no plaintext listener.」改为说明默认 HTTPS、`tls=false` 经反代终止 TLS 走明文；在 `[server]` 段补 `tls` 键注释（缺省=true=HTTPS，仅反代后设 false）。
- [X] T015 全量回归与门禁：`unshare -rUn sh -c 'ip link set lo up && go test ./...'` 全绿（SC-005）；`gofmt -l internal/server/config internal/server/app`、`go vet ./internal/server/config/... ./internal/server/app/...`、`staticcheck ./internal/server/config/... ./internal/server/app/...` 干净；对照 quickstart.md C1–C6 矩阵自检。

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001)**: 无依赖，先跑。
- **Foundational (T002→T003)**: 依赖 Setup；**阻塞所有用户故事**（共享 `TLSEnabled()`）。
- **US1 (T004-T005)**: 依赖 T003。MVP。
- **US2 (T006-T008)**: 依赖 T003；**不依赖 US1**（改 `Validate()`，与 app.go 监听分叉正交）。
- **US3 (T009-T012)**: 依赖 T003；其中 **T012 依赖 US1 的 T005**（在明文分支内加告警），T009-T011 不依赖 US1。
- **Polish (T013-T015)**: 依赖全部故事完成。

### File-Conflict Serialization（同文件，非 [P]）

- `config.go`：T003 → T008 → T011 串行。
- `config_test.go`：T002 → T006 → T009 串行。
- `app.go`：T005 → T012 串行。
- `app_test.go`：T004 → T007 → T010 串行。

### Parallel Opportunities

- 各故事的「单元测试(config_test.go)」与「集成测试(app_test.go)」属不同文件，可并行起草：T007 与 T006、T010 与 T009 各对 [P]。
- Polish 的 T013（DESIGN.md）与 T014（config.toml.example）不同文件，可 [P]。
- 逻辑上 US2 与 US1 互不依赖（仅文件冲突串行）；若分人协作，可在 Foundational 完成后并行推进 US1（app.go）与 US2（config.go Validate），最后合并测试文件改动。

---

## Parallel Example: User Story 2

```bash
# US2 的两类测试不同文件，可并行起草（均应先红 / 锁定既有行为）：
Task: "T006 Validate 证书条件化单元测试 in internal/server/config/config_test.go"
Task: "T007 缺省=HTTPS + TLS 缺证书硬失败 集成验收 in internal/server/app/app_test.go"
```

---

## Implementation Strategy

### MVP First (Setup + Foundational + User Story 1)

1. T001 Setup → T002/T003 Foundational（字段 + `TLSEnabled()`，红→绿）。
2. US1（T004 红 → T005 绿）：明文模式端到端可用 = MVP，可演示「反代后明文 HTTP」。

### Incremental Delivery

1. Setup + Foundational → 共享开关原语就位。
2. + US1 → 明文服务（MVP）。
3. + US2 → 锁定安全默认 / 缺证书硬失败（关键安全护栏）。
4. + US3 → 非回环明文绑定告警。
5. Polish：DESIGN.md/config 注释同步 + 全量回归 + lint。

### Notes

- 红→绿：T002/T009 经「引用未定义方法编译失败」达红；T004 经「裸 http 连 TLS 监听器失败」达红；T006 经「明文+空证书断言在旧无条件 Validate 下失败」达红（参照宪法 II 与 020 惯例）。
- US2 的「实现」即把安全默认与硬失败/忽略语义落到 `Validate()` 条件化（T008）；其端到端价值由 T007 验收锁定。
- 客户端、WireGuard 数据面、nftables、打包零改动（FR-009）——本特性不产生此类任务。
