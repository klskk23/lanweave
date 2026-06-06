# Contract: i18n 包 API + 偏好键 + 选择器行为

桌面应用无网络/CLI 对外接口;此契约约束**包内 API 面**与**客户端内部约定**,供 tasks/implement 与测试对齐。

## C1 — `internal/client/i18n` 包 API(非 gui tag)

```go
type Lang string
const (
    En    Lang = "en"
    ZhCN  Lang = "zh-Hans"
)

// Resolve 由偏好与系统 locale 推出生效语言(纯函数,可无头测试)。
// pref ∈ {"", "en", "zh-Hans"}(未知值按 "" 处理);sysLocale 为 BCP-47 串。
func Resolve(pref, sysLocale string) Lang

// Init 据 Resolve 结果设活动目录,启动期调用一次(在任何 UI 构建前)。
func Init(pref, sysLocale string)

// T 返回 key 在活动语言下的文案;缺失回退英文,再缺失返回 key 本身;
// 提供 args 时按文案中的占位以 fmt.Sprintf 格式化。
func T(key string, args ...any) string
```

**契约要点**
- `Resolve`:具体 `pref` 压过 `sysLocale`;空 `pref` 时 `sysLocale` 前缀 `zh*`→`ZhCN`,否则 `En`。
- `T`:活动目录命中 → 否则 en 目录 → 否则原样返回 `key`;**绝不返回空串占位**。
- 目录经 `//go:embed en.json zh-Hans.json` 内嵌;包 `init` 解析为 `map[string]string`。
- 线程模型:`Init` 在启动期单次设置,之后 `T` 只读 → 无需加锁。

## C2 — 翻译目录文件契约

- 路径:`internal/client/i18n/en.json`、`internal/client/i18n/zh-Hans.json`。
- 格式:扁平 JSON 对象 `{"<key>": "<text>", ...}`,UTF-8。
- **键集一致**(不变量):`keys(en) == keys(zh-Hans)`,单测双向断言(SC-004)。
- 占位:句中运行时值用 `%s`/`%q`/`%d` 等 `fmt` 动词;中英占位**数量与顺序一致**。
- 禁含敏感值(密钥/口令/邀请码/指纹)。

## C3 — 偏好键契约

- 键:`ui.language`(Fyne Preferences,app `com.lanweave.client`)。
- 值域:缺省/空 = 跟随系统;`en`;`zh-Hans`。
- 写:具体语言用 `SetString`;跟随系统用 `RemoveValue`。
- 读:`StringWithFallback("ui.language", "")`;未知值按空处理。
- **不得**写入 `state.Record`/state.json(FR-007)。

## C4 — 语言选择器行为契约(`ui/lang_select.go`,gui)

- 构造一个 `widget.Select`,选项固定三项:`跟随系统` / `English` / `中文`(选项标签本身经 `i18n.T` 本地化:键 `lang.followSystem` 等)。
- 初始选中:由当前偏好回推(空→「跟随系统」,`en`→English,`zh-Hans`→中文)。
- `OnChanged`:
  - 选具体语言 → `SetString("ui.language", 对应值)`;
  - 选「跟随系统」→ `RemoveValue("ui.language")`;
  - 之后弹**信息框**「该设置将在下次启动后生效」(`i18n.T("lang.restartNotice")`),**不**重绘当前视图。
- 复用同一构造:向导 `render()` 顶区一处、面板页脚一处,读写**同一**偏好键,行为一致(FR-003)。

## C5 — 启动序(`main.go`,gui)

1. `a := app.NewWithID("com.lanweave.client")`(既有)。
2. `pref := a.Preferences().StringWithFallback("ui.language", "")`。
3. `i18n.Init(pref, string(lang.SystemLocale()))` —— **在任何 `ui.NewWizard`/`ui.NewPanel` 之前**。
4. 其余启动逻辑不变。
