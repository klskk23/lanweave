# Contract: 子网宣告端点（030 新增 HTTP 面）

> 全部 AuthRequired（bearer）。zone 上下文鉴权沿用现有语义：调用者非该 zone 成员 → 404 `not_found`
> （与 zoneMembers 一致，不泄露 zone 存在性）。全端点共有 429 `rate_limited` / 500 `internal_error`
> / 401 `unauthorized`。**新端点与新错误码必须同步进 openapi.yaml 与 `knownErrorCodes`（029 哨兵强制）。**

## POST /api/v1/zones/{name}/announcements

宣告子网到 zone（或为既有宣告增加本 zone 挂接）。

- **Body**: `{"node_id": 3, "subnet": "192.168.1.0/24"}`
- **201**: `{"id": 7, "node_id": 3, "node_name": "router", "owner": "alice", "subnet": "192.168.1.0/24", "synthetic": "100.100.1.0/24"}`
  - 同节点同子网已有宣告时复用其合成段（不重复分配），仅新增挂接，仍 201。
- **错误**:

| HTTP | code | 场景 |
|---|---|---|
| 400 | `validation_error` | body 不合法 / 缺字段 / subnet 非法 CIDR 文本 |
| 400 | `subnet_invalid` | 非 RFC1918；前缀 < /16 或 > /30；与 VPN 池或合成池重叠 |
| 404 | `not_found` | 调用者非 zone 成员；node 不存在或非本人；node 不是该 zone 成员 |
| 409 | `platform_unsupported` | node.platform ≠ openwrt |
| 409 | `subnet_overlap` | 与同一节点现存宣告重叠 |
| 409 | `announce_limit_reached` | 用户宣告数达配额（admin 豁免；0=无限） |
| 409 | `validation_error`* | 已挂接本 zone（幂等冲突）→ 实为 409 `already_attached`？见下注 |
| 503 | `announce_disabled` | `announce.pool` 未配置 |
| 503 | `synthetic_pool_exhausted` | 池内无等长空闲块 |

> 注：重复挂接同一 zone 处理为**幂等 200/201 返回现状**（与 join 重复语义对齐由 plan 落地时按 joinZone 现状裁决），不新增错误码。

- **数据面副作用**: peer AllowedIPs += 合成段（全量替换式重算）；`zone_<id>_routes` += 合成段。

## DELETE /api/v1/zones/{name}/announcements/{id}

撤销宣告在本 zone 的挂接。

- **204**: 挂接删除；若为最后一个挂接，宣告本体与合成段同事务回收。
- **错误**: 404 `not_found`（非成员 / 宣告不存在 / 宣告未挂接本 zone / 宣告节点非本人**且**调用者非 zone owner——owner 可移除本 zone 任意挂接，对齐踢人权限）。
- **数据面副作用**: `zone_<id>_routes` -= 合成段；若本体回收则 peer AllowedIPs 重算收缩。

## GET /api/v1/zones/{name}/announcements

查询 zone 的宣告清单（任何成员可见，FR-008）。

- **200**: `{"announcements": [{"id":7,"node_id":3,"node_name":"router","owner":"alice","subnet":"192.168.1.0/24","synthetic":"100.100.1.0/24"}]}`
  - 池未配置时返回 `{"announcements": []}`（查询不报 `announce_disabled`）。
- **错误**: 404 `not_found`（非成员）。

## 既有端点变更（向后兼容）

- `POST /api/v1/nodes`: body 增可选 `platform`（`^[a-z0-9-]{1,32}$`，缺省 `unknown`；非法值 400 `validation_error`）。旧客户端不传 → unknown，注册行为不变（FR-012）。
- `GET /api/v1/nodes`: 响应每节点增 `platform` 字段。
- 级联触达（行为强化，无接口变更）：`leave`/`kick`/`DELETE node`/`DELETE zone`/`DELETE user` 现须同步清理宣告挂接与（必要时）宣告本体 + 数据面。

## 回归契约

- 全部既有端点路径/方法/请求/响应（除上述加字段）不变；`unshare -rUn` 全量测试绿。
- openapi.yaml 路由集合一致性测试（029）将因 3 个新端点而红 → 文档同步是本切片任务的一部分，不是可选项。
