# Tasks: Swagger / OpenAPI 文档页面

**Input**: Design documents from `/specs/029-swagger-ui/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/（docs-endpoints.md、openapi-coverage.md）, quickstart.md

**Tests**: 宪法 Principle II（NON-NEGOTIABLE）：每个 user story ≥1 验收测试；测试先写、先红后绿。本切片不触碰 SQLite/nftables/WireGuard 内核边界，集成测试复用 `internal/server/api` 现有 httptest + 真 SQLite store 装具（禁 mock 红线不破）。

**Organization**: 按 user story 分组；US1（文档页交互）为 MVP。

## Format: `[ID] [P?] [Story] Description`

## Phase 1: Setup

**Purpose**: 引入 vendor 资产，后续全部离线构建

- [X] T001 Vendor swagger-ui-dist：取 5.x 最新稳定版（pin 死），提取 `swagger-ui.css`、`swagger-ui-bundle.js` 与上游 LICENSE（Apache-2.0）放入 `internal/server/api/docs/assets/`；新建 `internal/server/api/docs/assets/README.md` 记录版本号、官方 SHA256、复现下载步骤（research.md D2）

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 路由表真源与配置开关——三个 story 共用的前置

**⚠️ CRITICAL**: 本阶段完成前不开任何 user story

- [X] T002 路由表重构：新建 `internal/server/api/routes.go` 定义 `type route{pattern string; handler http.Handler}` 与 `routes(h *handlers, opts Options) []route`（21 个现有注册逐行搬入，healthz/notFound 按现状保留）；改 `internal/server/api/router.go` 的 `NewRouter` 为遍历该表注册。**注册行为逐字节不变**（FR-011，research.md D3）；跑 `go test ./internal/server/api/...` 全绿后才算完成
- [X] T003 [P] 配置开关：`internal/server/config/config.go` 的 `ServerConfig` 增 `APIDocs *bool`（`toml:"api_docs"`）+ nil-safe `APIDocsEnabled() bool`（镜像 `TLSEnabled`，data-model.md §1）；`internal/server/config/config_test.go` 增三态用例（缺省=true / 显式 true / 显式 false），先写先红

**Checkpoint**: 路由表与开关就绪，US1/US2/US3 可并行开工

---

## Phase 3: User Story 1 - 第三方开发者通过文档页面探索并调用 API (Priority: P1) 🎯 MVP

**Goal**: `/api/docs/` 提供自包含 Swagger UI，渲染全部 21 个操作，支持 Authorize + try-it-out

**Independent Test**: 起服务（开关缺省），浏览器开 `/api/docs/`，21 个操作可见，填 token 后对 `/api/v1/me` try-it-out 得 200（quickstart.md §1）

### Tests for User Story 1 (REQUIRED) ⚠️ 先写先红

- [X] T004 [US1] 集成测试（开启态）：新建 `internal/server/api/docs_integration_test.go`（package api，复用现有测试装具构造 Options{APIDocs 开}）：`GET /api/docs/` → 200 + `text/html`；`GET /api/docs` → 301 → `/api/docs/`；`GET /api/docs/openapi.yaml` → 200 + `application/yaml` + 非空体；`GET /api/docs/swagger-ui.css`、`/api/docs/swagger-ui-bundle.js` → 200；`GET /api/docs/no-such.js` → 与全局未知路径相同的 404 JSON；docs 路径计入全局限流（复用现有 RateLimit 测试模式，FR-012）（contracts/docs-endpoints.md）

### Implementation for User Story 1

- [X] T005 [P] [US1] 手写 `internal/server/api/docs/openapi.yaml` 骨架：`openapi: 3.0.3`、`info`（英文，version 1.0.0）、`servers: [{url: /}]`、`components.securitySchemes.bearerAuth`、`components.schemas` 全量（对照 `pkg/protocol/*.go` 的 json tag 逐一落 schema，含统一 `ErrorResponse`）、`components.responses` 公共错误（429 rate_limited / 500 internal_error / 401 unauthorized / 403 forbidden）（contracts/openapi-coverage.md「全局约定」「schemas 清单」；FR-009 示例值全虚构；FR-013 全英文）
- [X] T006 [US1] `openapi.yaml` 补全 21 个操作的 `paths`：严格按 contracts/openapi-coverage.md 端点矩阵逐行落（方法/路径/鉴权标注/成功状态码与 schema/端点特有错误码子集），落每行前对照对应 handler 源码复核
- [X] T007 [P] [US1] 定制 `internal/server/api/docs/assets/index.html`：相对引用同目录 css/js、`url: "openapi.yaml"`、无 topbar、title "lanweave API docs"（research.md D2，FR-007/008）
- [X] T008 [US1] 新建 `internal/server/api/docs/docs.go`（package docs）：`//go:embed openapi.yaml assets/*`；导出构造器返回各路径 handler（index/重定向/openapi.yaml/静态资产，Content-Type 按 contracts/docs-endpoints.md；未知资产委托给调用方 notFound）
- [X] T009 [US1] 装配：`internal/server/api/router.go`+`routes.go` 在 `Options.APIDocs == true` 时把 docs 路由（`GET /api/docs/`、`GET /api/docs`、`GET /api/docs/{file...}`）加入路由表（docs 自身不进一致性比对集合）；`api.Options` 增 `APIDocs bool` 字段；`internal/server/app/app.go` 传 `cfg.Server.APIDocsEnabled()`
- [X] T010 [US1] 跑 T004 全部转绿；`unshare -rUn go test ./...` 全量回归绿（FR-011）

**Checkpoint**: MVP 可演示——浏览器全流程走通 quickstart.md §1、§2

---

## Phase 4: User Story 2 - 运维通过配置开关控制文档暴露 (Priority: P2)

**Goal**: `api_docs = false` 时文档面从外部彻底不可见，与不存在路径不可区分；业务 API 零感知

**Independent Test**: 关闭开关起服务，`curl -i` 对比 docs 路径与任意未知路径响应逐字节一致（quickstart.md §3）

### Tests for User Story 2 (REQUIRED) ⚠️ 先写先红

- [X] T011 [US2] 集成测试（关闭态）：在 `internal/server/api/docs_integration_test.go` 增 Options{APIDocs 关}用例：对 `/api/docs`、`/api/docs/`、`/api/docs/openapi.yaml`、`/api/docs/swagger-ui-bundle.js` 的响应（状态码、Content-Type、body 字节）与 `GET /api/v1/does-not-exist` **逐字节相同**（FR-006，data-model.md 不变量 1）；同一关闭态下任一业务端点照常工作（FR-011/SC-004 抽样）

### Implementation for User Story 2

- [X] T012 [US2] 确认关闭语义实现即「不注册」（T009 的 gating 分支），T011 转绿；若有偏差在 routes.go 修正——禁止任何「注册后 handler 内判开关」路径（research.md D4）
- [X] T013 [P] [US2] `config.toml.example` 增 `api_docs` 键：注释说明缺省=开启、`false`=关闭且与 404 不可区分、生产建议（D5/D7）

**Checkpoint**: US1+US2 独立可验：开/关两态行为均有自动化覆盖

---

## Phase 5: User Story 3 - 工具链消费机器可读的 API 描述文件 (Priority: P3)

**Goal**: `openapi.yaml` 语法合法、与服务器实际路由集合双向一致，可供 codegen/测试工具直接导入

**Independent Test**: `curl /api/docs/openapi.yaml` 产物过标准 OpenAPI lint 零 error（quickstart.md §6）；一致性测试绿

### Tests for User Story 3 (REQUIRED) ⚠️ 先写先红

- [X] T014 [P] [US3] 单元测试：新建 `internal/server/api/docs/docs_test.go`：嵌入的 openapi.yaml 可被 `gopkg.in/yaml.v3` 解析；`openapi` 字段前缀 `3.`；`info.title`/`paths` 非空（research.md D6）
- [X] T015 [P] [US3] 一致性测试：新建 `internal/server/api/openapi_consistency_test.go`（package api）：解析嵌入文档 `paths` 得 `(method, path)` 集合，与 `routes()` 路由表集合**双向全等**（healthz 无方法前缀归一为 GET；`/` 兜底与 `/api/docs/*` 排除）；文档内全部 `error` 枚举值 ⊆ 服务端 20 个已知错误码全集（FR-010，contracts/openapi-coverage.md「一致性测试口径」）

### Implementation for User Story 3

- [X] T016 [US3] 修复 T014/T015 暴露的文档-路由差异直至全绿（漂移哨兵自此生效：后续任何端点增删未同步文档，CI 必红）

**Checkpoint**: 三个 story 全部独立可验

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T017 [P] DESIGN.md 修订（宪法 D7，同 PR）：§11 已知风险新增「API 文档页默认开启、无鉴权，暴露 API 形状（不含数据/机密；`api_docs=false` 可关，关后与不存在不可区分）」；控制面章节补一句可选 `/api/docs` 文档页
- [X] T018 [P] `go.mod`：`gopkg.in/yaml.v3` 由 indirect 升 direct（仅测试 import 所需），`go mod tidy`
- [X] T019 lint 门禁：`gofmt -l .`、`go vet ./...`、`staticcheck ./...` 全清；`unshare -rUn go test ./...` 全绿
- [ ] T020 quickstart.md 人工矩阵逐条执行并勾选（§1 浏览器全流程、§2 离线、§3 关闭逐字节、§4 反代、§6 文档 lint 与机密抽查）；结果记录回 quickstart.md
  > 部分完成（见 quickstart「执行记录」）：curl 级 §1/§3、§5 全量测试、§6 redocly lint 已过；剩余浏览器渲染/Authorize/Try-it-out、离线 DevTools、反代三项需真实环境人工验收

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (P1)**: 无前置
- **Foundational (P2)**: 依赖 Setup —— 阻塞全部 story
- **US1 (P3)**: 依赖 Foundational；T005/T007 可与 T004 并行起，T008 依赖 T001/T005/T007，T009 依赖 T002/T003/T008，T010 收尾
- **US2 (P4)**: 依赖 US1 的 T009（gating 分支在其中）；T011 可在 T009 后立即写
- **US3 (P5)**: T014 依赖 T005；T015 依赖 T002+T006；互相 [P]
- **Polish (P6)**: T017/T018 随时可做（[P]）；T019/T020 必须最后

### Parallel Opportunities

```text
Foundational:  T002 ∥ T003
US1 起步:      T004 ∥ T005 ∥ T007（三个不同文件）
US3 测试:      T014 ∥ T015
Polish:        T017 ∥ T018
```

### Within Each Story

- 测试任务（T004/T011/T014/T015）先写、确认红，再做实现任务
- T010/T012/T016 是各 story 的「转绿」收尾闸门

---

## Implementation Strategy

**MVP = Phase 1→3（T001–T010）**：交付「默认开启的完整文档页」，即可演示 SC-001 全链路。
**增量 2 = Phase 4（T011–T013）**：补上生产关闭能力（US2）。
**增量 3 = Phase 5（T014–T016）**：上防漂移哨兵（US3，FR-010 的长期价值所在）。
**收尾 = Phase 6**：宪法强制的 DESIGN.md 同步 + 门禁 + 人工矩阵。

单人串行按 T001→T020 顺序即可；每完成一个逻辑组提交一次。

---

## Notes

- 全部改动集中在服务端；`internal/client`、`cmd/lanweave-client` 零触碰（FR-011）。
- T006 是体量最大的任务（21 个操作的 paths）；落每个操作前必须对照 handler 源码复核错误码子集，contracts/openapi-coverage.md 是规约但代码是最终真源。
- T002 重构是本切片唯一触碰既有行为的点，其安全性由「现有 api 包测试全绿」定义。
