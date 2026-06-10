# Implementation Plan: Swagger / OpenAPI 文档页面

**Branch**: `029-swagger-ui` | **Date**: 2026-06-10 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/029-swagger-ui/spec.md`

## Summary

服务端新增可选的 API 文档暴露面：手写一份覆盖全部 21 个控制面操作的 OpenAPI 3.0 YAML（英文，紧贴 `pkg/protocol` schema，逐端点标注实际错误码与 bearer/admin 鉴权），与 vendor 的 swagger-ui-dist 资产一起 `go:embed` 进 `lanweaved`，在 `/api/docs/` 下离线自包含地提供交互式文档页。`[server] api_docs` 三态 `*bool` 开关（缺省=开启，镜像 021 TLS 模式）；关闭时不注册路由，与未知路径逐字节同 404。`router.go` 注册重构为路由表（行为零变化），该表与 openapi.yaml 的 `(method,path)` 集合做双向一致性测试，实现「忘同步文档则 CI 红」。

## Technical Context

**Language/Version**: Go 1.26（仓库现状）

**Primary Dependencies**: 标准库 `net/http`/`embed`；`gopkg.in/yaml.v3`（已是间接依赖，升为直接、仅测试 import）；vendor swagger-ui-dist 5.x 静态文件（非 Go 依赖，Apache-2.0，入仓）。**零新增 Go 运行时依赖。**

**Storage**: 无（不新增表、不迁移；唯一持久面是 TOML 新键 `server.api_docs`）

**Testing**: `go test`（unit：YAML 解析/路由一致性/错误码子集；integration：httptest + api 包现有真 SQLite 装具）；`unshare -rUn go test ./...` 全量回归；quickstart.md 手工验收矩阵

**Target Platform**: Linux 服务端（lanweaved）；客户端零改动

**Project Type**: web-service（现有服务端的增量切片）

**Performance Goals**: 沿用宪法 IV 预算；docs 路径为内存静态资产 serve，P50 远低于 100ms 读预算；嵌入资产 ~1.5MB，常驻内存预算（<100MB）无感

**Constraints**: 离线自包含（无外部资源）；反代/明文 HTTP 兼容（全相对 URL）；关闭=与不存在不可区分；现有 API 行为零回归

**Scale/Scope**: 21 个已存在操作的文档化 + 4 个新静态路由；改动集中在 `internal/server/api`（含新 docs 子目录）、`config`、`app` 装配、`config.toml.example`、DESIGN.md

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 适用性与符合方式 | 结论 |
|---|---|---|
| I. Code Quality | 路由表重构是「数据替代重复代码」，注册行为逐字节不变；docs 子包单一职责（嵌入+serve）；零散 env 不引入；无 panic 路径 | PASS |
| II. Testing Standards | 三层齐备（见 Technical Context Testing）；不触碰 SQLite/nft/WG 内核边界，集成测试沿用 api 包真实 store 装具，禁 mock 红线不破；每个 user story ≥1 验收测试（US1→quickstart+集成 200 断言、US2→开关一致性测试、US3→YAML 可解析+一致性测试） | PASS |
| III. UX Consistency | 仅服务端，Windows 客户端零改动；文档页错误形态复用统一 JSON 信封 | PASS（弱适用） |
| IV. Performance | 静态内存 serve；冷启动增量为 embed 解包（µs 级）；预算全部不受影响 | PASS |
| Security & Operational | 文档不含机密（FR-009 + 测试抽查）；默认暴露「API 形状」登记进 DESIGN.md §11（D7，同 PR）；输入面仅 GET 静态路径；无新 crypto；单实例假设不受影响 | PASS（§11 修订随 PR） |
| Workflow Gates | spec→plan→tasks→implement 全走；DESIGN.md/§11 与 config.toml.example 同 PR 修订；ROADMAP 合并时勾选 | PASS |

**Post-Phase-1 re-check**: 设计未引入新违规；路由表重构是本计划唯一触碰既有代码的点，由全量现有测试兜底。无 Complexity Tracking 条目。

## Project Structure

### Documentation (this feature)

```text
specs/029-swagger-ui/
├── plan.md              # 本文件
├── research.md          # Phase 0（D1~D7 决策）
├── data-model.md        # Phase 1（配置字段/静态工件/路由表/不变量）
├── quickstart.md        # Phase 1（人工验收矩阵）
├── contracts/
│   ├── docs-endpoints.md     # 新增 HTTP 面契约
│   └── openapi-coverage.md   # openapi.yaml 内容规约（21 操作×错误码矩阵）
└── tasks.md             # Phase 2（/speckit-tasks 产出，非本命令）
```

### Source Code (repository root)

```text
internal/server/api/
├── router.go            # 改：逐行注册 → 遍历路由表；签名/行为不变
├── routes.go            # 新：route 表定义（单一真源）
├── docs/                # 新子包：嵌入与 serve
│   ├── docs.go          # //go:embed openapi.yaml assets/*; Handler() 工厂
│   ├── docs_test.go     # YAML 可解析/结构断言
│   ├── openapi.yaml     # 手写 OpenAPI 3.0（英文）
│   └── assets/
│       ├── index.html   # 定制（相对 url、无 topbar）
│       ├── swagger-ui.css           # vendor（pin 版本）
│       ├── swagger-ui-bundle.js     # vendor（pin 版本）
│       ├── LICENSE                  # Apache-2.0 副本
│       └── README.md                # 版本/SHA256/更新步骤
├── openapi_consistency_test.go  # 新：双向路由一致性 + 错误码子集（package api）
└── docs_integration_test.go     # 新：开/关行为、404 逐字节、限流

internal/server/config/config.go   # 改：ServerConfig.APIDocs *bool + APIDocsEnabled()
internal/server/config/config_test.go
internal/server/app/app.go         # 改：把 APIDocsEnabled() 传入 api.Options
config.toml.example                # 改：api_docs 键 + 注释
DESIGN.md                          # 改：§11 风险登记 + 控制面一句
```

**Structure Decision**: 沿用现有单体服务端布局；docs 作为 `internal/server/api/docs` 子包（单一职责：嵌入并 serve 文档工件），路由表留在 api 包内（注册与一致性测试同包共享，无需导出）。

## Complexity Tracking

无宪法违规，本表为空。

## 已接受的限制（设计期显式登记）

- **字段级 schema 防漂移不自动化**：端点集合（method+path）双向 CI 强制；请求/响应字段漂移靠 review + `pkg/protocol` 单一真源紧贴维护（spec Assumptions、research D3）。
- **healthz 注册不限方法的历史行为保留**：文档按 GET 记载并说明 405 行为，一致性比对做 GET 归一（research D3）。
- **swagger-ui-dist 以静态文件 vendor**：升级靠手动重跑 README 步骤，无自动追新（与 016 图标产物入仓同先例）。
