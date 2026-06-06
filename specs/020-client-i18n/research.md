# Research: client-i18n (Phase 0)

源码核验基于 `fyne.io/fyne/v2@v2.7.4` 与 `github.com/jeandeaual/go-locale@v0.0.0-20250612000132`(GOMODCACHE 实读)。

## R1 — Fyne `lang` 能否让「用户手动所选语言」在启动时压过系统 locale?(ROADMAP 必验点)

**结论:不能(经公开 API)。这是本特性最关键的取向决策。**

- `fyne.io/fyne/v2/lang` 导出面仅:`SystemLocale`、`Localize`/`L`、`LocalizeKey`/`X`、`LocalizePlural`/`N`、`LocalizePluralKey`/`XN`、`AddTranslations`、`AddTranslationsForLocale`、`AddTranslationsFS`。**无 `SetLanguage`/`SetLocale` 之类设置器。**
- 实际选语言的 `setupLang(lang string)`(`localizer = i18n.NewLocalizer(bundle, lang)`)**不导出**(注释:仅供单测覆盖系统语言)。
- 唯一触发它的 `updateLocalizer()` 恒执行 `setupLang(closestSupportedLocale(locale.GetLocales()).LanguageString())`——**总是重新读系统 locale**;每次 `AddTranslations*` 末尾都会调用它,无注入点。
- `closestSupportedLocale` 用 `language.NewMatcher(translated)` 对系统 locale 取最近匹配;`translated` 在 `init()` 已含 `en`,无法经公开 API 移除,故「只注册 zh 以骗过 matcher」在英文系统上仍匹配到 en——覆盖失败。

**决策**:不依赖 Fyne `lang` 的自动 locale 选择来满足覆盖。引入自有 `internal/client/i18n`:显式解析「偏好→否则系统 locale」,自带 `T(key, args...)` 查表 + 英文回退。Fyne `lang` 仅保留 `SystemLocale()` 用于**读取** OS locale。

**Rationale**:确定性覆盖、跨平台一致、纯逻辑可无头测试。
**Alternatives considered**:(a) 直接 `lang.L`——证伪,无覆盖能力;(b) 启动前设 `LANG`/`LC_*`——见 R2,Windows 无效;(c) fork/vendor Fyne lang 暴露 setter——超出依赖卫生与「最小改动」原则。

## R2 — `go-locale` 在 Windows 上读什么?环境变量覆盖可行吗?

**结论:Windows 走 Win32,忽略环境变量;env 覆盖在目标平台无效。**

- `locale_windows.go`:`GetLocales()`→`GetLocale()`→`getWindowsLocale()`,依次调 kernel32 的 `GetUserDefaultLocaleName`、`GetSystemDefaultLocaleName`;**全程不读 `LANG`/`LC_ALL`/`LANGUAGE`**。
- 对比 `locale_unix.go` 确按 `LC_ALL`→`LC_MESSAGES`→`LANG`(+`LANGUAGE`)优先——但那是 Unix,与 Windows 客户端无关。

**决策**:放弃 env 覆盖路径(ROADMAP「落地可能靠启动前设 LANG env」的设想在 Windows 证伪)。覆盖逻辑由 R1 的自有包承担。
**含义**:`i18n.Resolve(pref, sysLocale)` 中 `sysLocale` 由调用方经 `lang.SystemLocale()`(Win32 读)取得并传入,包内不直接触 OS,便于无头注入测试。

## R3 — 系统 locale 如何映射到我们支持的两种语言?

**决策**:`lang.SystemLocale()` 返回形如 `zh-Hans`/`zh-Hans-CN`/`en-US`/`fr-FR` 的 BCP-47 串。映射规则:前缀大小写不敏感以 `zh` 开头 → `zh-Hans`;其余一律 → `en`(默认回退,含未支持语言如法语)。
**Rationale**:v1 仅中/英;英文为统一回退(spec Edge Cases + Assumptions)。规则为纯函数,表驱动单测。

## R4 — 偏好存储:Fyne Preferences 三态语义

**决策**:键 `ui.language`。读 `Preferences().StringWithFallback("ui.language", "")`;写具体语言 `SetString("ui.language", "en"|"zh-Hans")`;选「跟随系统」`RemoveValue("ui.language")`(回到空=auto)。
- 核验 `Preferences` 接口确有 `String`/`StringWithFallback`/`SetString`/`RemoveValue`。
- app 已 `app.NewWithID("com.lanweave.client")`,`Preferences()` 在 `app.New` 后即可读,**先于** state.json 存在 → 满足 FR-007「首次向导即可读、与 onboarding 解耦」。
**Alternatives considered**:存 `state.Record`——违反硬约束(首次向导时 state.json 不存在),否决。

## R5 — 翻译目录格式与查表/回退

**决策**:每语言一份扁平 JSON `{"key":"text"}`(自有加载器 `map[string]string`,不用 go-i18n 的 `{"other":...}` 包裹,最简)。`en.json` 为键的权威来源兼缺键回退;`zh-Hans.json` 键集须与 en 完全一致(单测断言差集为空,SC-004)。`T(key, args...)`:活动目录命中→否则 en→否则返回 key 本身;有 `args` 则 `fmt.Sprintf`。
**Rationale**:UI 现有插值用 Go 拼接/`fmt.Sprintf`(如 `"This device: "+name`),迁移为 `T("panel.thisDevice", name, ip)` 配 `"This device: %s (%s)"` 即可,无需 go-i18n 模板。

## R6 — 句中插值与枚举文案的迁移边界(守住 FR-009)

**决策**:
- 句中含运行时值的字符串(设备名、IP、zone 名、服务器 URL、指纹)→ 翻译值用 `%s`/`%q` 占位,`ui` 层 `fmt.Sprintf`/`T(key, args...)`。
- 连接状态行 `"Status: "+st.String()`:`st.String()` 属 `tunnel` 包(非 ui),**不改它**;在 ui 层按枚举映射本地化键 `T("status.connected"|"connecting"|"disconnected")`,既本地化又不越界到 tunnel 包(守 FR-009)。`onlineText`(online/offline)本就在 ui,直接键化。
**Rationale**:维持「用户可见字符串只在 ui 层映射」,服务端与底层零改动。

## R7 — 切换生效时机与重绘

**决策**:重启生效;选语言后写偏好并弹「该设置将在下次启动后生效」信息框,**不**实时重绘当前视图(FR-006)。不做强制自动重启——VPN 客户端自动重启会意外断隧道(spec Assumptions)。
**Alternatives considered**:运行时实时重绘——需为 wizard/panel 增「重建当前视图」管线,收益极低(切语言极低频),否决。

## R8 — 选择器放置与共享构造

**决策**:抽 `ui/lang_select.go` 提供共享构造:`widget.Select{跟随系统, English, 中文}`,当前选中由偏好回推(空→跟随系统),`OnChanged` 写偏好(具体语言 `SetString`,跟随系统 `RemoveValue`)+ 弹重启提示。向导侧置于 `render()` 顶区(与既有 insecure/pinned 提示同列布局),面板侧置于页脚(与 Log out / 信任指示同区)。两处复用同一构造 → 同一偏好读写,行为一致(FR-003)。
