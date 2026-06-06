package winelevate

import "testing"

func TestCommandLine(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"empty", nil, ""},
		{"single flag", []string{"--insecure"}, "--insecure"},
		{"two flags", []string{"--insecure", "--verbose"}, "--insecure --verbose"},
		{"value with spaces", []string{"--config", `C:\Program Files\lanweave`}, `--config "C:\Program Files\lanweave"`},
		{"value with quote", []string{`a"b`}, `"a\"b"`},
		{"empty argument", []string{""}, `""`},
		{"mixed", []string{"--insecure", "x y"}, `--insecure "x y"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandLine(tc.args); got != tc.want {
				t.Errorf("commandLine(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
