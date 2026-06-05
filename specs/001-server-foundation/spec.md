# Feature Specification: Server Foundation

**Feature Branch**: `001-server-foundation`

**Created**: 2026-06-05

**Status**: Draft

**Input**: User description: "server-foundation 完成本项目的第一个feat目标，包括toml配置加载，SQLite 打开 + 迁移框架，结构化日志，HTTPS 服务骨架，admin bootstrap：TOML 首位 admin 明文密码 → argon2id hash 入库，全局 rate.Limiter 中间件框架"

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — 运维从单个配置文件启动服务并确认存活 (Priority: P1)

运维拿到 `lanweaved` 二进制和一份示例 `config.toml`，按文档调整监听地址、TLS 证书路径、数据目录、网段与 admin 凭证后，启动服务。启动完成后，运维通过健康检查端点确认服务已就绪、TLS 握手成功。

**Why this priority**: 没有它，后续所有 feature 都没有运行宿主。这是整个系统进入"可观察、可触达"状态的最小闭环。

**Independent Test**: 在干净的 Linux 虚拟机上 dpkg-install 或手动放置二进制 → 写入一份 `config.toml` → 启动服务 → `curl --cacert <ca> https://<host>:<port>/api/v1/healthz` 返回 200 与一个简短的 JSON 状态。无需任何其他 feature 完成。

**Acceptance Scenarios**:

1. **Given** 一份语法正确、字段完整的 `config.toml`，**When** 启动服务，**Then** 进程进入运行状态，监听配置的 HTTPS 端口，且健康检查在 5 秒内返回 200。
2. **Given** 一份缺失必填字段（如 TLS 证书路径）的 `config.toml`，**When** 启动服务，**Then** 进程在启动阶段以非零退出码失败，并在日志中输出指明缺失字段的人类可读错误。
3. **Given** 一份指向不存在的证书文件的 `config.toml`，**When** 启动服务，**Then** 进程启动失败并明确报错"TLS 证书加载失败"，不监听任何端口。
4. **Given** 服务正在运行，**When** 进程收到 SIGTERM，**Then** 服务在 10 秒内优雅停机，关闭 HTTPS 监听并释放 SQLite 句柄。

---

### User Story 2 — 首次启动时从配置写入 admin 账号 (Priority: P1)

运维在 `config.toml` 中声明首位 admin 的用户名与明文密码。服务首次启动检测到数据库中无对应用户时，把密码经强 hash 后写入数据库，并标记该用户为 admin。后续启动时同名用户已存在 → 跳过创建，不覆盖数据库中的现有 hash。

**Why this priority**: feature 002（邀请码注册与登录）依赖一个能产出邀请码的 admin；admin 用户必须在首次启动后立即可用，否则整条注册链无法启动。优先级与 P1（启动）并列，但二者可独立验证。

**Independent Test**: 启动服务一次 → 用 sqlite CLI 打开 DB → 查 `users` 表 → 看到一行：用户名匹配配置、`is_admin = true`、`password_hash` 非空且不是配置中的明文。修改配置中的 admin 密码、重启服务 → 数据库中的 hash 未变（幂等）。

**Acceptance Scenarios**:

1. **Given** DB 中无任何用户，配置中声明 admin 用户名 `alice` 与明文密码 `secret`，**When** 服务启动，**Then** `users` 表新增一行：`username='alice'`、`is_admin=true`、`password_hash` 是 `secret` 的强 hash 结果，并且配置文件未被修改。
2. **Given** DB 中已存在用户名为 `alice` 的用户（无论 hash 是否对应配置中的密码），**When** 服务再次启动，**Then** 该用户的 hash 与 admin 标记保持不变，启动日志声明"admin 已存在，跳过 bootstrap"。
3. **Given** 配置中 admin 密码字段为空字符串或缺失，**When** 服务启动，**Then** 启动失败并报错"admin 凭证未提供"，进程不进入服务状态。
4. **Given** admin bootstrap 写入用户后，**When** 用任意工具核对 `password_hash`，**Then** 该值不可逆，且对相同明文密码每次计算结果不同（即 hash 算法采用随机 salt）。

---

### User Story 3 — 运维通过结构化日志观察服务行为 (Priority: P2)

服务运行期间所有关键事件（启动、配置加载、admin bootstrap、HTTP 请求、错误、关停）输出为结构化日志条目，包含时间戳、级别、事件名、相关字段。运维可以用通用日志工具检索、过滤、聚合。

**Why this priority**: 没有结构化日志也能跑 happy path，但任何排错、监控、审计都依赖它。MVP 必备，但可在 P1 跑通后立即叠上。

**Independent Test**: 启动服务 → 触发若干请求（包括成功与失败）→ 收集 stdout/journald 输出 → 每行是合法 JSON（或运维选定的统一结构化格式），含至少 `timestamp`、`level`、`msg` 三个字段，错误日志额外含 `error` 字段。

**Acceptance Scenarios**:

1. **Given** 服务运行中，**When** 收到一次 `/api/v1/healthz` 请求，**Then** 日志中出现一条 INFO 级别条目，含 HTTP 方法、路径、状态码、耗时。
2. **Given** 服务运行中，**When** 配置加载失败或 admin bootstrap 失败，**Then** 日志中出现 ERROR 级别条目，含失败原因字段，且进程退出码非零。
3. **Given** 服务输出的任意一条日志，**When** 用 JSON 解析器（或运维选定格式的解析器）处理，**Then** 解析成功且能从中提取至少 `timestamp` 与 `level` 字段。

---

### User Story 4 — 服务在过载时主动拒绝以保护自己 (Priority: P3)

任何客户端（恶意或失控）对服务端 API 短时间高频请求时，服务返回标准的"请求过多"响应并丢弃多余请求，保护 CPU、DB、内存不被打爆，不影响正常用户。

**Why this priority**: 是 MVP 安全护栏。即便阈值很宽松，框架到位后续 feature 才能复用。但与 P1/P2 相比，缺它服务仍能正常服务低负载流量。

**Independent Test**: 启动服务 → 用脚本以远超配置阈值的速率打 `/api/v1/healthz` → 超出阈值的请求收到统一的"过载"响应（含 Retry-After 或等效信息），低于阈值的请求正常返回 200。

**Acceptance Scenarios**:

1. **Given** 服务运行、限流阈值已配置，**When** 单一来源以低于阈值的速率请求，**Then** 全部请求按正常路径处理。
2. **Given** 单一来源以高于阈值的速率请求，**When** 超出令牌桶容量，**Then** 多余请求被拒绝，返回 HTTP 429 状态码并附带可读提示。
3. **Given** 高频请求停止后等待一段时间，**When** 重新发起请求，**Then** 令牌恢复，请求恢复正常处理。

---

### Edge Cases

- **配置文件不存在或路径错误**：进程启动失败并报错，明确告知运维查阅的路径。
- **数据目录无写权限**：服务启动失败，日志告知 `/var/lib/lanweave/` 权限问题。
- **TLS 证书已过期 / 私钥不匹配**：服务启动失败，明确指出 TLS 握手准备失败。
- **DB 文件被外部进程占有锁**：服务报错并退出（而非无限重试）。
- **migration 半完成（系统断电）**：下次启动时 migration 框架能继续未完成的步骤，或以可重入方式恢复，绝不静默跳过。
- **TOML 中 admin 密码包含特殊字符（含 Unicode）**：hash 后正确保存，未来登录时仍能比对成功。
- **日志输出目标不可达**（如 stdout 被截断管道、磁盘满）：服务不崩溃，但内部计数器应记录丢失。
- **限流耗尽场景**：健康检查端点是否豁免？默认不豁免（保证一致性），但行为应明确并文档化。

---

## Requirements *(mandatory)*

### Functional Requirements

**配置加载**

- **FR-001**: 服务 MUST 从命令行参数指定的单一配置文件加载所有运行时设置；缺省参数指向约定路径。
- **FR-002**: 配置 MUST 包含至少以下字段：HTTPS 监听地址与端口、TLS 证书与私钥路径、数据目录路径、WireGuard 网段（供后续 feature 读取）、JWT 签名密钥（供后续 feature 读取）、初始 admin 用户名与密码。
- **FR-003**: 服务 MUST 在启动阶段校验配置字段：必填非空、文件路径存在且可读、网段格式合法；任何校验失败 MUST 以非零退出码失败并输出说明。
- **FR-004**: 配置文件 MUST NOT 在启动后被服务进程修改。

**持久化与迁移**

- **FR-005**: 服务 MUST 在启动时打开（或新建）配置指定路径的 SQLite 数据库文件。
- **FR-006**: 服务 MUST 携带一个迁移框架，使每次启动时按声明的顺序把数据库结构推进到最新版本，已执行的迁移不会重复执行。
- **FR-007**: 迁移失败 MUST 阻止服务进入对外服务状态，并在日志中明确指出失败步骤。
- **FR-008**: 数据库 MUST 至少包含 `users` 表，字段足以承载 user_id、username（唯一）、password_hash、is_admin、created_at 等本 feature 的需求；其他实体表的引入延后到对应 feature。

**HTTPS 服务骨架**

- **FR-009**: 服务 MUST 监听配置中声明的地址与端口，仅以 HTTPS 提供服务；明文 HTTP MUST NOT 被启用。
- **FR-010**: 服务 MUST 暴露一个健康检查端点 `/api/v1/healthz`，返回 200 与一个 JSON 体（含至少服务状态字段），不需要身份验证。
- **FR-011**: 服务 MUST 对收到的 SIGTERM 与 SIGINT 优雅停机：停止接受新连接、给正在处理的请求合理的完成时间、关闭数据库连接。
- **FR-012**: 服务 MUST 在 HTTPS 监听失败（端口被占用、证书加载失败等）时以非零退出码退出，附带可读错误。

**Admin Bootstrap**

- **FR-013**: 服务首次启动 MUST 检测 `users` 表中是否已存在与配置 admin 用户名相同的用户；如不存在，则将该用户写入并标记为 admin。
- **FR-014**: 写入 admin 时，密码 MUST 经强 hash 函数（带随机 salt、抵御暴破的现代算法）处理后存储；配置中的明文密码 MUST NOT 直接写入数据库或日志。
- **FR-015**: 已存在同名 admin 的情况下，服务 MUST 跳过创建并保留数据库中的现有 hash，不基于配置重置密码。
- **FR-016**: admin bootstrap 失败（DB 写入异常、hash 失败、配置缺失）MUST 阻止服务进入对外服务状态。

**结构化日志**

- **FR-017**: 服务的所有日志输出 MUST 是机器可解析的结构化条目，至少含时间戳、级别、消息三个字段。
- **FR-018**: 服务 MUST 在以下事件至少各产出一条日志：进程启动、配置加载完成、迁移执行（每条迁移分别记录）、admin bootstrap 决策（创建或跳过）、HTTPS 监听就绪、每次 HTTP 请求完成、任何 ERROR 或致命错误、停机开始与完成。
- **FR-019**: 日志 MUST NOT 包含明文密码、原始 TLS 私钥、JWT 签名密钥等敏感凭证字段。
- **FR-020**: 日志的级别 MUST 可由配置或环境变量调整（至少 debug / info / warn / error 四档）。

**速率限制框架**

- **FR-021**: 服务 MUST 在所有 HTTP API 路径上挂载一个全局速率限制中间件，使用令牌桶或等效策略。
- **FR-022**: 阈值（每秒允许请求数与桶容量）MUST 来自配置；若配置未给出，使用一个文档化的默认值（如 100 req/s、桶容量 200）。
- **FR-023**: 当请求被限流时，响应 MUST 返回 HTTP 429 状态码并附带可读消息；客户端可重试。
- **FR-024**: 限流框架 MUST 提供后续 feature 可复用的扩展点（如允许某些路径自定义阈值），但本 feature 不要求实现路径级差异化。

### Key Entities

- **服务配置**：单一 TOML 文件，承载所有运行时参数；启动后只读。运维拥有，进程仅消费。
- **数据库**：单一 SQLite 文件，承载所有持久化状态；本 feature 仅初始化与 `users` 表。
- **用户（user）**：本 feature 中仅承载 admin 一条。属性包括用户名（唯一）、密码 hash、是否 admin、创建时间。其他属性与流程留给后续 feature。
- **迁移记录**：数据库内一张框架自维护的表，记录已执行的 schema 变更，保证迁移幂等。

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 干净的目标 Linux 主机上，运维从拿到二进制与配置示例到"健康检查返回 200"用时不超过 10 分钟。
- **SC-002**: 服务从启动到 `/api/v1/healthz` 第一次返回 200 的耗时不超过 3 秒（典型硬件、已就位的证书与数据目录）。
- **SC-003**: 服务收到 SIGTERM 后在 10 秒内完全退出且不丢失对外可见的成功响应。
- **SC-004**: 服务在持续 1000 req/s 的健康检查请求下不崩溃；超阈值请求被合规拒绝，正常用户响应延迟变化不超过基准值 2 倍。
- **SC-005**: 运维抽样任意 10 条服务日志，100% 可被通用 JSON 解析器解析并提取出时间戳与级别字段。
- **SC-006**: 首次启动后，使用通用密码 hash 检验工具能确认 `users.password_hash` 是被广泛认可的现代 hash 格式，且不可从中还原明文。
- **SC-007**: 同一配置下连续重启服务 10 次，第 1 次出现 admin 创建日志，后续 9 次均出现 admin 跳过日志，数据库中 admin 行的 hash 值在 10 次之间完全一致。

---

## Assumptions

- 服务运行平台为 Linux（与 DESIGN.md §2 一致）；Windows 仅作为客户端，不在本 feature 范围。
- 配置格式为 TOML，落盘路径与字段布局对齐 DESIGN.md §10.3 的示例。
- 持久化采用 SQLite（DESIGN.md §1）；本 feature 不引入其他数据库。
- 密码 hash 算法采用 argon2id 或社区共识等价物（DESIGN.md §4.3）；本 feature 不约束具体参数，留给实现侧选择默认值。
- 结构化日志输出到 stdout，由 systemd / journald 收集（DESIGN.md §10.6）；本 feature 不内置文件轮转。
- TLS 证书由运维自行准备（自签或 LE）；本 feature 不实现自动续签（已列入 v1.1，见 DESIGN.md §12）。
- 全局速率限制采用进程内令牌桶；本 feature 不引入跨实例共享状态（系统当前为单实例部署）。
- 配置中的 admin 明文密码在文档中已声明风险（DESIGN.md §11），运维负责文件权限与防泄。
- 本 feature 不实现任何业务 API（注册、登录、node、zone 全部留给后续 feature）；仅提供基础设施与健康检查。
- 数据库 schema 仅引入 `users` 表（含 admin bootstrap 所需字段）；其他实体表（invites、nodes、zones、zone_members）由后续 feature 通过新的 migration 引入。
