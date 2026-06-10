# Quickstart: Swagger / OpenAPI 文档页面 (029)

> 验收/冒烟步骤。自动化测试之外的人工矩阵（宪法 II acceptance 层）。

## 执行记录（2026-06-10，实现期）

已在开发机用**真实 lanweaved 二进制**（`unshare -rUn` + `tls=false` loopback）完成 curl 级验收：

- §1 curl 部分 ✅：缺省键 → `/api/docs/` 200 text/html、`openapi.yaml` 200、`/api/docs` 301、两资产 200 且 Content-Type 正确；login→`/api/v1/me` 全链路 200（SC-001 的 API 侧）。
- §3 ✅：`api_docs = false` → 4 个 docs 路径与 `/api/v1/nope-xyz` 响应**逐字节一致**（仅 Date 头不同）；healthz / login 照常 200。
- §6 文档 lint ✅：`npx @redocly/cli lint` 对 `openapi.yaml` **零 error**（修复点：5 个公开端点补显式 `security: []`）；机密模式由 `docs_test.go` 自动钳制（BEGIN/eyJ/PRIVATE KEY）。
- §5 ✅：gofmt / go vet / staticcheck 全清；`unshare -rUn bash -c 'ip link set lo up && go test ./...'`（CI 同款命令）全绿。

**剩余人工项**（需浏览器/真实部署环境，发布前过一遍）：§1 浏览器渲染 + Authorize + Try-it-out、§2 DevTools 离线核验、§4 反代场景。

## 前置

- 一台能跑 `lanweaved` 的 Linux（或 `unshare -rUn` 测试环境），按 `config.toml.example` 配好。
- 一个可用账号（bootstrap admin 即可）。

## 1. 默认开启（缺省键）

```bash
# config.toml 不写 api_docs
sudo systemctl restart lanweaved   # 或手动起服务
curl -k https://<host>/api/docs/openapi.yaml | head    # 期望: YAML 文档开头
```

浏览器打开 `https://<host>/api/docs/`：

- [ ] 页面渲染 Swagger UI，标题 lanweave API
- [ ] 端点列表完整（auth / nodes / zones / admin 分组可见，共 21 个操作）
- [ ] 任一端点展开可见请求字段、响应 schema、错误码枚举（英文描述）
- [ ] 点 **Authorize**，粘贴登录拿到的 access token
- [ ] 对 `GET /api/v1/me` 点 **Try it out → Execute**，返回 200 与本人信息（SC-001 链路）
- [ ] 对 `GET /api/v1/nodes` 同样成功

## 2. 离线自包含（FR-008 / SC-005）

- [ ] 在浏览器 DevTools Network 面板硬刷新 `/api/docs/`：所有请求均指向本服务器，无任何第三方域名
- [ ] （可选强验）断开测试机外网后重复步骤 1，页面与 try-it-out 全部正常

## 3. 关闭开关（FR-005 / FR-006 / SC-003）

```bash
# config.toml: [server] api_docs = false，重启服务
curl -sk -i https://<host>/api/docs/          > /tmp/docs.txt
curl -sk -i https://<host>/api/v1/nope-xyz    > /tmp/unknown.txt
diff <(grep -v -i '^date:' /tmp/docs.txt) <(grep -v -i '^date:' /tmp/unknown.txt)
```

- [ ] diff 为空（状态行、Content-Type、body 全同；仅 Date 头随时间不同）
- [ ] `/api/docs/openapi.yaml`、`/api/docs/swagger-ui-bundle.js` 同样表现为该 404
- [ ] 业务 API 照常：登录、列 nodes 全部正常（FR-011）

## 4. 明文 HTTP / 反代（FR-007，021 场景）

```bash
# config.toml: tls = false, listen = "127.0.0.1:8080"；前面摆 nginx/Caddy 终止 TLS
```

- [ ] 经反代的 `https://<proxy-host>/api/docs/` 正常渲染，try-it-out 打向反代地址（相对 URL 生效）

## 5. 自动化回归

```bash
gofmt -l . && go vet ./... && staticcheck ./...
unshare -rUn go test ./...
```

- [ ] 全绿，含新增：openapi.yaml 解析/路由一致性/错误码子集（unit）、开关开/关响应一致性与限流（integration）

## 6. 文档质量抽查

- [ ] `openapi.yaml` 用任一标准校验器（如 `npx @redocly/cli lint` 或 swagger-editor 粘贴）零 error（SC US3）
- [ ] 文档内搜索不到任何真实机密样式内容（密码、私钥、真实 token；示例全为虚构占位，FR-009）
