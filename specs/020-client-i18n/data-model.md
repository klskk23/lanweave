# Data Model: client-i18n (Phase 1)

本特性不涉及数据库实体,仅有两个客户端侧概念 + 一张语言解析表。

## 实体

### 语言偏好 (Language Preference)

- **存储位置**:Fyne Preferences,键 `ui.language`(app ID `com.lanweave.client`)。**不入 state.json**(FR-007)。
- **取值(三态)**:
  | 存储值 | 含义 |
  |--------|------|
  | (键缺省 / 空串) | 跟随系统(首次默认) |
  | `en` | 强制英文 |
  | `zh-Hans` | 强制简体中文 |
- **读**:`Preferences().StringWithFallback("ui.language", "")`。
- **写具体语言**:`SetString("ui.language", "en"|"zh-Hans")`(FR-004,跨重启保留)。
- **复位跟随系统**:`RemoveValue("ui.language")`(FR-005)。
- **校验/回退**:未知或损坏值视同空(跟随系统)(spec Edge Cases)。

### 翻译目录 (Translation Catalog)

- **形态**:每语言一份扁平 JSON `map[string]string`(`{"key":"text"}`),经 `//go:embed` 嵌入 `internal/client/i18n`。
- **成员**:`en.json`(键的**权威来源** + 缺键回退)、`zh-Hans.json`。
- **不变量**:`keys(zh-Hans) == keys(en)`(单测断言双向差集为空,SC-004);任一缺键时 `T` 回退英文,绝不暴露原始键名或空白(FR-008)。
- **内容约束**:仅静态界面/错误文案,**不含任何密钥、口令、邀请码、指纹等敏感值**(宪法 Secrets;敏感/运行时值由 `ui` 层经 `%s` 占位注入)。

## 语言解析 (Effective Language Resolution)

纯函数 `Resolve(pref, sysLocale string) Lang`,启动期定一次(`Init` 据此设活动目录):

| pref | sysLocale | 生效语言 |
|------|-----------|----------|
| `zh-Hans` | (任意) | 中文 |
| `en` | (任意) | 英文 |
| 空 | 以 `zh` 开头(如 `zh-Hans-CN`) | 中文 |
| 空 | 其他(如 `en-US`、`fr-FR`) | 英文(默认回退) |

- `pref` 来自语言偏好实体;`sysLocale` 由 `main.go` 经 `lang.SystemLocale()`(Windows 走 Win32)取得并传入(便于无头注入测试)。
- 具体 `pref` **压过** `sysLocale`(FR-004);空 `pref` 才看系统(FR-002)。

## 状态流转

```
首次安装(无 pref) ──Resolve──> 跟随系统(zh→中文 / 其余→英文)
   │ 用户选 English/中文 (SetString) + 弹「下次启动生效」
   ▼
具体语言 pref ──重启──Resolve──> 所选语言(无视系统 locale)
   │ 用户选「跟随系统」(RemoveValue) + 弹「下次启动生效」
   ▼
回到「无 pref」──重启──> 跟随系统
```

切换写偏好即时持久,但**界面在下次启动才变**(FR-006,不实时重绘)。
