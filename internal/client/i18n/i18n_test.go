package i18n

import "testing"

func TestResolveFollowsSystem(t *testing.T) {
	cases := []struct {
		pref, sys string
		want      Lang
	}{
		{"", "zh-Hans-CN", ZhCN},
		{"", "zh-CN", ZhCN},
		{"", "en-US", En},
		{"", "fr-FR", En},
		{"", "", En},
	}
	for _, c := range cases {
		if got := Resolve(c.pref, c.sys); got != c.want {
			t.Errorf("Resolve(%q,%q)=%q want %q", c.pref, c.sys, got, c.want)
		}
	}
}

func TestTFallback(t *testing.T) {
	// A missing key renders as the key itself, never blank.
	if got := T("no.such.key"); got != "no.such.key" {
		t.Errorf("T(missing)=%q want the key itself", got)
	}

	// Active hit with an fmt placeholder.
	savedActive, savedEn := active, enFallback
	defer func() { active, enFallback = savedActive, savedEn }()
	active = map[string]string{"greet": "Hello %s"}
	if got := T("greet", "Bob"); got != "Hello Bob" {
		t.Errorf("T(greet,Bob)=%q want %q", got, "Hello Bob")
	}

	// Missing in active, present in the English fallback.
	active = map[string]string{}
	enFallback = map[string]string{"only.en": "English only"}
	if got := T("only.en"); got != "English only" {
		t.Errorf("T(only.en)=%q want fallback %q", got, "English only")
	}
}

func TestResolvePreferenceWins(t *testing.T) {
	if got := Resolve("zh-Hans", "en-US"); got != ZhCN {
		t.Errorf("Resolve(zh-Hans,en-US)=%q want %q (preference beats system)", got, ZhCN)
	}
	if got := Resolve("en", "zh-Hans-CN"); got != En {
		t.Errorf("Resolve(en,zh-Hans-CN)=%q want %q (preference beats system)", got, En)
	}
}

func TestSelectorMapping(t *testing.T) {
	if PrefForIndex(1) != "en" || PrefForIndex(2) != "zh-Hans" {
		t.Errorf("PrefForIndex concrete = %q/%q want en/zh-Hans", PrefForIndex(1), PrefForIndex(2))
	}
	if IndexForPref("en") != 1 || IndexForPref("zh-Hans") != 2 {
		t.Errorf("IndexForPref concrete = %d/%d want 1/2", IndexForPref("en"), IndexForPref("zh-Hans"))
	}
}

func TestFollowSystemReset(t *testing.T) {
	// Index 0 is the follow-system sentinel: it stores no preference, and an empty or
	// unrecognized stored value selects it.
	if PrefForIndex(0) != "" {
		t.Errorf("PrefForIndex(0)=%q want empty (follow system)", PrefForIndex(0))
	}
	if IndexForPref("") != 0 || IndexForPref("bogus") != 0 {
		t.Errorf("IndexForPref(empty/bogus) = %d/%d want 0/0", IndexForPref(""), IndexForPref("bogus"))
	}
	// With no preference, resolution returns to the system locale.
	if Resolve("", "en-US") != En || Resolve("", "zh-Hans-CN") != ZhCN {
		t.Errorf("empty-pref Resolve did not follow system: %q/%q", Resolve("", "en-US"), Resolve("", "zh-Hans-CN"))
	}
}

func TestCatalogParity(t *testing.T) {
	en, zh := catalogs[En], catalogs[ZhCN]
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("key %q present in en.json but missing from zh-Hans.json", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("key %q present in zh-Hans.json but missing from en.json", k)
		}
	}
}
