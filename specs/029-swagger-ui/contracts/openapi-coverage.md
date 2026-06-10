# Contract: openapi.yaml 覆盖矩阵（029）

> 手写 `internal/server/api/docs/openapi.yaml` 的内容规约。来源：`router.go` 路由 +
> 各 handler 实际 `WriteJSONError` 调用（2026-06-10 普查）。实现时以代码为准逐端点复核。
> 文档语言：英文（FR-013）。schema 紧贴 `pkg/protocol` 类型。

## 全局约定

- `servers: [{url: /}]`（相对路径，反代安全）
- `components.securitySchemes.bearerAuth`: http bearer (JWT)
- 统一错误信封 `ErrorResponse {error: string, message: string}`
- **全端点共有错误**（在 components.responses 定义并引用，矩阵内不再重复）：
  - `429 rate_limited`（全局限流）
  - `500 internal_error`（serverError / panic Recoverer）
- 需鉴权端点共有：`401 unauthorized`；admin 端点额外 `403 forbidden`
- 文档中的示例值一律显式虚构（FR-009）：如 `user@example`、`AAAA…AAA=` 形式占位公钥、`100.127.0.7`

## 端点矩阵（21 个操作）

| # | Method/Path | 鉴权 | 成功 | 端点特有错误码 |
|---|---|---|---|---|
| 1 | GET /api/v1/healthz | 无 | 200 `HealthResponse` | 405 `method_not_allowed`（非 GET） |
| 2 | POST /api/v1/login | 无 | 200 `LoginResponse` (access+refresh) | 400 `validation_error`；401 `invalid_credentials` |
| 3 | POST /api/v1/register | 无 | 201 `RegisterResponse` | 400 `validation_error`（含密码策略文案）；422 `invite_invalid`；409 `username_taken` |
| 4 | POST /api/v1/refresh | 无（RT 在 body） | 200 `RefreshResponse` | 400 `validation_error`；401 `invalid_refresh_token` |
| 5 | POST /api/v1/logout | 无（RT 在 body） | 204 | 400 `validation_error` |
| 6 | GET /api/v1/me | bearer | 200 `MeResponse` | — |
| 7 | GET /api/v1/server | bearer | 200 `ServerInfoResponse` | — |
| 8 | POST /api/v1/nodes | bearer | 201 `NodeResponse` | 400 `validation_error`；409 `node_name_taken` / `pubkey_taken` / `device_limit_reached`；503 `pool_exhausted` |
| 9 | GET /api/v1/nodes | bearer | 200 `NodeListResponse` | — |
| 10 | DELETE /api/v1/nodes/{id} | bearer | 204 | 404 `not_found` |
| 11 | POST /api/v1/zones | bearer | 201 `ZoneResponse` | 400 `validation_error`；404 `not_found`（auto-join node 非本人）；409 `zone_name_taken` / `zone_limit_reached` |
| 12 | GET /api/v1/zones | bearer | 200 `ZoneListResponse` | — |
| 13 | POST /api/v1/zones/{name}/join | bearer | 200 | 400 `validation_error`；403 `invalid_zone_or_password`；404 `not_found` |
| 14 | POST /api/v1/zones/{name}/leave | bearer | 204 | 400 `validation_error`；404 `not_found` |
| 15 | GET /api/v1/zones/{name}/members | bearer | 200 `ZoneMembersResponse` | 404 `not_found` |
| 16 | PATCH /api/v1/zones/{name} | bearer (owner) | 200 | 400 `validation_error`；403 `forbidden`；404 `not_found` |
| 17 | DELETE /api/v1/zones/{name} | bearer (owner) | 204 | 403 `forbidden`；404 `not_found` |
| 18 | DELETE /api/v1/zones/{name}/members/{node_id} | bearer (owner) | 204 | 403 `forbidden`；404 `not_found` |
| 19 | DELETE /api/v1/admin/users/{id} | bearer (admin) | 204 | 403 `forbidden` / `cannot_delete_self`；404 `not_found`；409 `last_admin` |

Admin 邀请码两端点：

| # | Method/Path | 鉴权 | 成功 | 端点特有错误码 |
|---|---|---|---|---|
| 20 | POST /api/v1/admin/invites | bearer (admin) | 201 `InviteResponse` | 403 `forbidden` |
| 21 | GET /api/v1/admin/invites | bearer (admin) | 200 `InviteListResponse` | 403 `forbidden` |

> 注：矩阵共 21 行，与 `router.go` 当前全部注册一一对应；一致性测试比对的就是本矩阵对应的
> paths 集合，本矩阵与路由表为最终真源。

## components.schemas 清单（对应 `pkg/protocol`）

`ErrorResponse`、`HealthResponse`、`LoginRequest/LoginResponse`、`RegisterRequest/RegisterResponse`、
`RefreshRequest/RefreshResponse`、`LogoutRequest`、`MeResponse`、`ServerInfoResponse`、
`CreateNodeRequest/NodeResponse/NodeListResponse`、
`CreateZoneRequest/ZoneResponse/ZoneListResponse/ZoneMembersResponse`、
`JoinZoneRequest/LeaveZoneRequest/ChangeZonePasswordRequest`、
`CreateInviteRequest(若有)/InviteResponse/InviteListResponse`。
实现时逐一对照 `pkg/protocol/*.go` 的 json tag 与字段可空性。

## 一致性测试口径（FR-010）

- 比对集合 = 本矩阵 21 个 `(method, path)` ⟷ `package api` 路由表。
- healthz 注册无方法前缀 → 归一为 GET 参与比对（405 行为在文档以 method_not_allowed 说明）。
- `/`（notFound 兜底）与 `/api/docs/*` 自身不参与。
- 文档内出现的全部 `error` 枚举 ⊆ 已知错误码全集（20 个，见 research.md D6）。
