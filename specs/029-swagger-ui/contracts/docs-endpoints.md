# Contract: 文档暴露端点（029 新增 HTTP 面）

> 本切片唯一新增的对外接口。仅当 `server.api_docs` 生效为开启时存在；
> 关闭时以下全部路径的响应与任意未知路径**逐字节一致**（404 `{"error":"not_found","message":"The requested resource was not found."}`）。

## GET /api/docs/

- **鉴权**: 无
- **响应**: `200 OK`，`Content-Type: text/html; charset=utf-8`
- **内容**: Swagger UI 页面（嵌入的定制 index.html），以相对路径引用同目录 css/js 与 `openapi.yaml`，不发起任何外部网络请求。

## GET /api/docs（无尾斜线）

- **响应**: `301 Moved Permanently` → `Location: /api/docs/`（相对跳转，反代安全）

## GET /api/docs/openapi.yaml

- **鉴权**: 无
- **响应**: `200 OK`，`Content-Type: application/yaml`
- **内容**: 嵌入的 OpenAPI 3.0 文档原文（英文，FR-013），可被标准 OpenAPI 校验工具零错误解析（SC 对应 US3）。

## GET /api/docs/{asset}

- `swagger-ui.css` → `200`，`text/css`
- `swagger-ui-bundle.js` → `200`，`text/javascript`
- 未知资产名 → 与全局未知路径相同的 404 JSON（不引入第二种 404 形态）

## 横切约束

| 约束 | 行为 |
|---|---|
| 限流 | 与业务 API 同一全局 `rate.Limiter`，超限 `429 {"error":"rate_limited"}`（FR-012） |
| 日志 | 走现有 RequestLogger（方法/路径/状态/时长） |
| panic 恢复 | 走现有 Recoverer |
| TLS / 明文 | 两种监听模式下行为一致（FR-007） |
| 缓存 | 不设置缓存头（保持最小面；浏览器默认行为即可） |

## 不变更项（回归契约）

- 全部 `/api/v1/*` 端点的路径、方法、请求/响应、错误码、状态码不变（FR-011）。
- `NewRouter` 函数签名不变；`Options` 仅新增 docs 开关字段。
