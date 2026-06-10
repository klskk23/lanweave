# Quickstart: 服务端子网宣告控制面 (030)

> 验收/冒烟步骤（宪法 II acceptance 层）。前置：lanweaved 已按 config.toml.example 配置，
> `[announce] pool = "100.100.0.0/16"` 已开，admin 账号可用。`$API` = 服务器地址，`$T` = bearer token。

## 执行记录（2026-06-11，实现期）

- §1–§6 的全部断言已由自动化测试逐条覆盖并全绿（CI 同款 `unshare -rUn bash -c 'ip link set lo up && go test ./...'`）：
  - §1 → `TestRegisterNodePlatform`；§2 → `TestAnnounceHappyPath`（含 wg/nft 状态断言）；§3 → `TestAnnounceSameSubnetCoexistence`+store 层并存矩阵；§4 → `TestAnnounceErrorMatrix`/`TestAnnounceDisabledAndExhausted` + config 三态测试；§5 → `TestAnnounceCascades` 六路径（DB/wg/nft 三处零残留 + 合成段复用）；§6 → `TestAnnounceRestartRebuild`。
- §7 端到端连通已自动化（`TestAnnounceEndToEnd`，专属 netns 拓扑 + 真内核 WG 中转）：成员→合成地址往返通、非成员丢弃、**反向新建丢弃**（FR-013 负断言）。
- openapi.yaml 已同步 3 端点 + 6 错误码（029 哨兵转绿），`npx @redocly/cli lint` 零 error。
- 实现期发现并修复的设计缺口：服务器需为合成池安装指向 wg 接口的内核路由（`EnsurePoolRoute`，已入 DESIGN §3.3b）。
- **剩余人工项**：真机两台 Linux/OpenWrt 客户机复刻 §7（032 交付后顺带）；生产配置改动后的 systemctl 重启冒烟（§6 命令行形态）。

## 1. 平台门禁与注册兼容（FR-001/002/012）

```bash
# 旧式注册（不带 platform）→ 成功，platform=unknown
curl -sk -X POST $API/api/v1/nodes -H "Authorization: Bearer $T" \
  -d '{"name":"old-style","wg_pubkey":"<pk1>"}'
# openwrt 节点注册
curl -sk -X POST $API/api/v1/nodes -H "Authorization: Bearer $T" \
  -d '{"name":"router","wg_pubkey":"<pk2>","platform":"openwrt"}'
```

- [ ] 两者均 201；`GET /api/v1/nodes` 分别显示 `"platform":"unknown"` / `"openwrt"`
- [ ] 用 unknown 节点宣告 → 409 `platform_unsupported`

## 2. 宣告全链路（US1）

```bash
# router 节点（id=N）已 join zone "home"
curl -sk -X POST $API/api/v1/zones/home/announcements -H "Authorization: Bearer $T" \
  -d '{"node_id":N,"subnet":"192.168.1.0/24"}'
```

- [ ] 201，返回的 `synthetic` 是 100.100.0.0/16 内的 /24
- [ ] `wg show wg-lanweave` 中 router 的 peer allowed-ips 含该合成段
- [ ] `nft list table inet lanweave` 出现 `zone_<id>_routes` interval set 含该段、对应 accept 规则、链头 `ct state established,related accept`
- [ ] `GET /api/v1/zones/home/announcements`（另一成员的 token）能看到 subnet→synthetic 映射
- [ ] 非成员 token 查询 → 404

## 3. 相同真实网段并存（US2 / SC-002）

- [ ] 另一节点（另一 zone 或同 zone）宣告同样的 `192.168.1.0/24` → 201，合成段不同
- [ ] 先建宣告的 peer allowed-ips 与 nft 状态逐字节不变（无抢占）
- [ ] 同一节点再宣告 `192.168.1.128/25` → 409 `subnet_overlap`
- [ ] 同节点同子网宣告到第二个 zone → 复用同一合成段（响应 synthetic 一致）

## 4. 校验与配额矩阵（SC-006）

- [ ] `8.8.8.0/24`（非 RFC1918）→ 400 `subnet_invalid`；`10.0.0.0/8`（< /16）与 `/31` → 400
- [ ] 与 VPN 池/合成池重叠的子网 → 400 `subnet_invalid`
- [ ] 配置 `max_announced_subnets_per_user = 1` 重启：第二条宣告 → 409 `announce_limit_reached`；admin 不受限；`= 0` 无限
- [ ] 注释掉 `[announce] pool` 重启：宣告 → 503 `announce_disabled`，清单查询返回空数组，其余 API 正常

## 5. 级联六路径（US3 / SC-003）——每条后检查三处零残留

逐一执行并断言（DB 行、`wg show` allowed-ips、`nft list` set 元素三处干净，合成段可被新宣告复用）：

- [ ] ① DELETE 挂接（最后一个）② 节点 leave zone ③ owner 踢出 ④ DELETE 节点 ⑤ owner 删 zone ⑥ admin 删用户
- [ ] 多 zone 挂接时只删一个挂接 → 另一 zone 与合成段完好

## 6. 重启重建（SC-004）

- [ ] 留 2+ 条宣告，`systemctl restart lanweaved` → `wg show` 与 `nft list` 与重启前一致

## 7. 端到端连通（SC-001，netns 模拟宣告端）

集成测试自动覆盖（app 层 netns 拓扑：成员 netns ping 合成地址 → 经服务器转发 → 宣告 netns 内手工 NETMAP+MASQUERADE 翻译 → 回程通；非成员同目标被丢弃）。真机演练可选：两台 Linux 客户机按 docs 配置后复刻。

## 8. 自动化回归

```bash
gofmt -l . && go vet ./... && staticcheck ./...
unshare -rUn bash -c 'ip link set lo up && go test ./...'
```

- [ ] 全绿，含 029 的 openapi 一致性测试（3 个新端点已同步进 openapi.yaml）
