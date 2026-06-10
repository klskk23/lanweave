# Research: Swagger / OpenAPI 文档页面 (029)

> Phase 0 产出。每条 = Decision / Rationale / Alternatives considered。
> 输入：spec.md（FR-001~013）、宪法 v1.0.1、现有代码（`internal/server/api/router.go`、`internal/server/config/config.go`、`pkg/protocol`）。

---

## D1 — OpenAPI 文档来源：手写 `openapi.yaml`，`go:embed` 进二进制

**Decision**: 在仓库手写一份 OpenAPI 3.0 YAML（21 个操作），`go:embed` 编进 `lanweaved`，运行时原样 serve。

**Rationale**:
- API 面小且已冻结（v1 设计冻结，FR-011 禁止改 API），手写一次性成本低、可控性最高——错误码子集（FR-002）、admin 权限标注（FR-003）这类语义信息只有手写能精确表达，注释生成器表达不了「该端点实际会返回哪几个错误码」。
- 零新运行时依赖，符合宪法 Principle I（小、明显、可逆）。
- 防漂移交给 D3 的自动化一致性测试，不依赖「生成」来保证同步。

**Alternatives considered**:
- **swaggo/swag 注释生成**：经典版只出 OpenAPI 2.0；v2 仍 beta；引入构建期工具 + 注释 DSL 侵入全部 handler，违背「不改现有 API 代码」的最小触面原则。拒绝。
- **运行时反射生成（swaggest/rest 等框架）**：要求 handler 改用框架类型签名，等于重写 api 包，FR-011 直接出局。拒绝。
- **手写 JSON**：YAML 可读性/可注释性优于 JSON，且 Swagger UI 原生支持加载 YAML。拒绝 JSON。

## D2 — Swagger UI 资产：vendor `swagger-ui-dist` 子集入仓 + `go:embed`

**Decision**: 把 swagger-ui-dist（pin 一个 5.x 版本，实现时取当时最新并在 README 记录版本与 SHA256）中实际需要的文件（`swagger-ui.css`、`swagger-ui-bundle.js`、定制 `index.html`，必要时 favicon）提交进仓库 `internal/server/api/docs/assets/`，连同其 Apache-2.0 LICENSE 副本；`go:embed` 后由服务端直接 serve。`index.html` 用**相对路径** `url: "openapi.yaml"` 加载文档。

**Rationale**:
- FR-008 要求完全自包含、离线可用 → CDN 出局。
- 相对路径加载满足 FR-007（反代/明文 HTTP 场景不依赖绝对地址）。
- 资产入仓与 016 图标产物入仓同一先例：构建不依赖网络，CI 可重现（宪法「build reproducible from go.sum」精神）。
- 体积 ~1.5MB 嵌入，服务端常驻内存预算（<100MB @1000 nodes）不受影响。

**Alternatives considered**:
- **CDN 引用**：违反 FR-008。拒绝。
- **Go 封装包（swaggest/swgui 等）**：多一个依赖，内部同样是 embed dist，还失去对 index.html（相对 URL、去 topbar）的控制。拒绝。
- **RapiDoc / Scalar / Stoplight Elements**：单文件更小，但「Swagger 页面」是 TODO 的字面需求，swagger-ui 的 Authorize + Try-it-out 是事实标准，生态认知成本最低。拒绝。
- **构建期下载（如 014 wintun 模式）**：本地 `go build` 将依赖网络，比一次性 vendor 差。拒绝。

## D3 — 防漂移（FR-010）：路由表抽成单一真源 + 包内一致性测试

**Decision**: 把 `router.go` 中逐行 `mux.Handle(...)` 的注册改写为遍历一个**路由表**（`[]route{pattern, handler}` 切片，pattern 即现有 `"METHOD /path"` 字符串，handler 含中间件包裹后的最终 handler）。注册行为逐字节不变。在 `package api` 内新增一致性测试：解析嵌入的 `openapi.yaml` 的 `paths`，与路由表导出的 `(method, path)` 集合做**双向全等**比对（文档多列、少列、方法不符均 fail）。

**Rationale**:
- 路由表既驱动注册又驱动测试 → 注册与文档比对共享同一数据，新增/删除端点而忘改文档时 CI 必红，这是 FR-010 的「自动化保证」。
- 测试在 `package api` 包内（现有 `api_test.go` 同包先例），无需导出新 API。
- 纯数据重构，`NewRouter` 对外签名与行为不变，FR-011 安全；现有 api 包测试全量回归兜底。

**Normalization 细则**:
- `/api/v1/healthz` 现注册**不限方法**（历史行为，FR-011 不动它）；文档侧只记 `GET`。比对时：无方法前缀的 pattern 映射为 GET。
- 兜底 `"/"`（notFound）与 docs 自身路径（`/api/docs/...`）不进比对集合。
- mux 通配 `{id}`、`{name}`、`{node_id}` 与 OpenAPI `{id}` 花括号语法一致，直接字符串比对。

**字段级 schema 防漂移**（spec Assumptions 已界定）: 不自动化。请求/响应字段漂移靠 code review + 文档紧贴 `pkg/protocol` 类型手工维护；`pkg/protocol` 是协议单一真源且改动必过 review。记入 plan 的已接受限制。

**Alternatives considered**:
- **黑盒探测（对文档中每个路径发请求断言非 404）**：只能查「文档多写」，查不出「服务端新增而文档漏写」。半套机制，拒绝。
- **AST 解析 router.go**：脆、不明显，违背 Principle I。拒绝。

## D4 — 暴露路径与关闭语义

**Decision**:
- `GET /api/docs/` → Swagger UI 页面（index.html）
- `GET /api/docs/openapi.yaml` → 文档原文（`application/yaml`）
- `GET /api/docs/{asset}` → 嵌入静态资产（js/css）
- `/api/docs`（无尾斜线）→ 301 重定向到 `/api/docs/`（仅开启时存在）
- **开启时才注册**这些 pattern；关闭时不注册 → 自然落入现有 `notFound`（`{"error":"not_found",...}`），与任意未知路径**逐字节一致**（FR-006）。
- 所有 docs 路由在 mux 内 → 自动吃到全局 RateLimit / Recoverer / RequestLogger 中间件（FR-012）。
- 无鉴权（spec Assumption：文档页公开浏览）。

**Rationale**: 「不注册 = 不存在」是实现 FR-006 最不费力且最不可能出错的方式——不存在「关闭分支返回手工 404」与真 404 漂移的可能。路径选 `/api/docs` 不与 `/api/v1/*` 冲突，保留版本无关位置。

**Alternatives considered**: 注册后在 handler 内判开关返回 404 —— 要手工保证响应逐字节一致，多一处可漂移点。拒绝。

## D5 — 配置开关：`[server] api_docs`，三态 `*bool`，缺省=开启

**Decision**: `ServerConfig` 增 `APIDocs *bool` (`toml:"api_docs"`) + nil-safe 方法 `APIDocsEnabled() bool { return s.APIDocs == nil || *s.APIDocs }`。`config.toml.example` 增注释行。Validate 无需新规则（bool 无非法值）。

**Rationale**: 与 021 `TLS *bool` 完全同款的既有模式（三态指针区分「未写」与「显式 false」）。用户已确认缺省=开启；显式 `api_docs = false` 关闭。

**Alternatives considered**: 普通 `bool`（零值=false 与「缺省开启」冲突，必须指针）；反转命名 `disable_api_docs`（双重否定可读性差，且项目已有 *bool 先例）。均拒绝。

## D6 — 测试策略（宪法 II 三层）

**Decision**:
- **Unit（`package api`，纯 Go）**：
  1. 嵌入的 openapi.yaml 可被 `gopkg.in/yaml.v3` 解析（已是间接依赖，升为直接——仅测试 import），`openapi` 字段为 `3.` 前缀，`info`/`paths` 非空；
  2. D3 双向路由一致性测试；
  3. 文档中出现的全部 `error` 枚举值 ⊆ 服务端已知错误码全集（防错误码拼写漂移）。
- **Integration（httptest，复用 api 包现有真 SQLite store 测试装具）**：开启 → `/api/docs/` 200 + `text/html`、`/api/docs/openapi.yaml` 200 + 非空体；关闭 → 对 docs 各路径的响应（状态码/Content-Type/body）与 `GET /api/v1/does-not-exist` **逐字节相同**；docs 路由计入全局限流（复用现有限流测试模式）。
- **Acceptance（quickstart.md 手工矩阵）**：浏览器打开页面、Authorize 填 token、try-it-out 调 `/api/v1/me` 看真实响应；断外网验证页面完整加载；`tls=false` 反代路径下复验。

**Rationale**: docs 功能本身不触碰 SQLite/nftables/WireGuard 内核边界，但集成测试沿用 api 包真实 store 装具（禁 mock 红线不破）。`unshare -rUn go test ./...` 全量保持绿即覆盖 FR-011 回归。

## D7 — DESIGN.md / 文档同步（宪法强制，同 PR）

**Decision**:
- DESIGN.md §11 已知风险新增一条接受项：**API 文档页默认开启、无鉴权，暴露「API 形状」**（不含任何数据/机密；运维可 `api_docs = false` 关闭；关闭后与不存在不可区分）。
- 控制面章节补一句：服务端可选暴露 `/api/docs` 文档页。
- `config.toml.example` 增 `api_docs` 键及注释。
- 文档文字全英文（spec Clarification / FR-013）。

**Rationale**: 宪法「Accepted risks register: DESIGN.md §11 是唯一登记处」+「DESIGN.md authority: 矛盾须同 PR 修订」。

## 解决的 NEEDS CLARIFICATION

| 项 | 结论 |
|---|---|
| 文档生成机制 | D1 手写 YAML + embed |
| 暴露路径 | D4 `/api/docs/` 族 |
| 字段级防漂移 | D3 显式不自动化（已接受限制），端点集合双向自动化 |
| UI 资产来源 | D2 vendor swagger-ui-dist 子集 |
| 开关键名/零值 | D5 `server.api_docs`，三态 *bool，nil=开启 |
