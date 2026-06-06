// Package i18n is the client's UI-string catalog and language resolver. It is deliberately
// free of any GUI (Fyne) dependency so the resolution and catalog-parity logic can be unit
// tested headlessly; the gui-tagged ui package calls T to render localized text.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Lang is a supported UI language tag.
type Lang string

const (
	En   Lang = "en"
	ZhCN Lang = "zh-Hans"
)

//go:embed en.json zh-Hans.json
var files embed.FS

var (
	catalogs   = map[Lang]map[string]string{}
	enFallback map[string]string // English: the key authority and the fallback for any miss
	active     map[string]string // the language chosen at startup; English until Init runs
)

// init loads the embedded catalogs. The //go:embed directive guarantees the files exist at
// build time and the catalog-parity test guarantees they parse, so a failure here is an
// authoring error caught before release — this is the documented panic-on-misuse path.
func init() {
	for _, l := range []Lang{En, ZhCN} {
		data, err := files.ReadFile(string(l) + ".json")
		if err != nil {
			panic("i18n: missing embedded catalog " + string(l) + ".json")
		}
		m := map[string]string{}
		if err := json.Unmarshal(data, &m); err != nil {
			panic("i18n: invalid catalog " + string(l) + ".json: " + err.Error())
		}
		catalogs[l] = m
	}
	enFallback = catalogs[En]
	active = enFallback
}

// Resolve picks the effective language. A concrete preference ("en" or "zh-Hans") wins over
// the system locale; an empty or unrecognized preference falls back to the system locale,
// where a "zh" prefix means Chinese and everything else (including unsupported locales) means
// English.
func Resolve(pref, sysLocale string) Lang {
	switch Lang(pref) {
	case En, ZhCN:
		return Lang(pref)
	}
	if strings.HasPrefix(strings.ToLower(sysLocale), "zh") {
		return ZhCN
	}
	return En
}

// Init selects the active catalog from the preference and system locale. It is called once at
// startup, before any UI is built; afterwards T is read-only, so no locking is needed.
func Init(pref, sysLocale string) {
	active = catalogs[Resolve(pref, sysLocale)]
}

// prefValues is the canonical language-selector order: follow-system, English, Chinese. The
// empty string means "follow the system locale" (no stored preference).
var prefValues = []string{"", string(En), string(ZhCN)}

// LabelKeys returns the selector option label keys in prefValues order, for the caller to
// localize with T.
func LabelKeys() []string {
	return []string{"lang.followSystem", "lang.english", "lang.chinese"}
}

// PrefForIndex maps a selector index to the preference value to store ("" clears the override
// back to follow-system). An out-of-range index falls back to follow-system.
func PrefForIndex(i int) string {
	if i < 0 || i >= len(prefValues) {
		return ""
	}
	return prefValues[i]
}

// IndexForPref maps a stored preference to its selector index; an empty or unrecognized value
// selects follow-system (index 0).
func IndexForPref(pref string) int {
	for i, v := range prefValues {
		if i == 0 {
			continue // skip the follow-system sentinel; it is the default below
		}
		if v == pref {
			return i
		}
	}
	return 0
}

// T returns key's text in the active language, falling back to English and then to the key
// itself, so a missing translation never renders as a blank or a raw key name. When args are
// supplied the resolved string is treated as an fmt format and rendered with them.
func T(key string, args ...any) string {
	s, ok := active[key]
	if !ok {
		if s, ok = enFallback[key]; !ok {
			s = key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}
