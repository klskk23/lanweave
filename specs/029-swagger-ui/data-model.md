# Data Model: Swagger / OpenAPI 文档页面 (029)

本切片**不新增任何数据库实体、不做 schema 迁移**。涉及的「数据」全部是静态工件与配置字段。

## 1. 配置字段（`internal/server/config`）

### `ServerConfig.APIDocs *bool` (`toml:"api_docs"`)

| 状态 | TOML | 含义 |
|---|---|---|
| nil | 键缺省 | **开启**（默认，用户已确认） |
| true | `api_docs = true` | 开启 |
| false | `api_docs = false` | 关闭：docs 路由不注册，路径表现为 not_found |

- nil-safe 读取方法：`APIDocsEnabled() bool`（镜像 `TLSEnabled()`）。
- `Validate()` 无新规则（布尔无非法值）。
- 与 `applyDefaults` 无交互（保持 nil，读取时判定——与 TLS 同款；不像 limits 那样物化默认值）。

## 2. 静态工件

### 2.1 OpenAPI 文档（`internal/server/api/docs/openapi.yaml`，go:embed）

手写、入仓、英文（FR-013）。顶层结构约束：

- `openapi: 3.0.x`
- `info`: title=lanweave API, version 与服务端版本解耦（文档自身版本化，记 `1.0.0`；服务端二进制版本是运行时注入的 ldflags，不写死进静态文档）
- `servers`: `[{url: /}]`（相对，FR-007 反代安全）
- `components.securitySchemes.bearerAuth`: `type: http, scheme: bearer, bearerFormat: JWT`
- `components.schemas`: 紧贴 `pkg/protocol` 的请求/响应类型 + 统一错误信封 `ErrorResponse {error, message}`
- `paths`: 21 个现有操作，逐端点列出其实际返回的错误码子集（见 contracts/openapi-coverage.md）

**一致性不变量**（自动化，FR-010）: `paths` 的 `(method, path)` 集合 ≡ `package api` 路由表集合（healthz 归一为 GET；`/` 兜底与 `/api/docs/*` 自身除外）。

### 2.2 Swagger UI 资产（`internal/server/api/docs/assets/`，go:embed）

| 文件 | 来源 | 说明 |
|---|---|---|
| `index.html` | 本仓定制 | 相对 `url: "openapi.yaml"`；无 topbar；title=lanweave API docs |
| `swagger-ui.css` | swagger-ui-dist（pin 版本） | 原样 vendor |
| `swagger-ui-bundle.js` | swagger-ui-dist（pin 版本） | 原样 vendor |
| `LICENSE` | swagger-ui 上游 | Apache-2.0 副本 |
| `README.md` | 本仓 | 记录 vendor 版本、SHA256、更新步骤 |

## 3. 代码内数据结构（非持久化）

### 3.1 路由表（`internal/server/api`，包内私有）

```go
type route struct {
    pattern string       // 现有 mux pattern 原文，如 "POST /api/v1/login"
    handler http.Handler // 已包好 AuthRequired/AdminRequired 的最终 handler
}
```

- `NewRouter` 由逐行 `mux.Handle` 改为遍历该表注册，**注册结果逐字节等价**（FR-011）。
- 同时是 D3 一致性测试的服务端真源。
- healthz 保持现状注册（pattern 无方法前缀，任意方法可达——历史行为不动）。

## 4. 状态转移

无。开关为启动时一次性读取（宪法：配置只在启动加载），运行期不热切换；改配置需重启——与全部现有配置一致。

## 5. 不变量汇总

1. `api_docs` 关闭 ⇒ 对 `/api/docs`、`/api/docs/`、`/api/docs/openapi.yaml`、任意 `/api/docs/*` 的响应与 `GET /api/v1/<不存在>` 逐字节一致（状态码、Content-Type、body）。
2. `api_docs` 任何取值 ⇒ 全部 `/api/v1/*` 业务端点行为不变（现有测试全绿）。
3. 文档端点集合 ≡ 路由表集合（双向，CI 强制）。
4. 文档中出现的错误码 ⊆ 服务端错误码全集（CI 强制）。
5. 嵌入资产 serve 时不发起任何外部网络请求（页面自包含）。
