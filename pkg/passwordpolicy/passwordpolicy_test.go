package passwordpolicy

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	// A 64-char compliant string: 'A','a','1' then 61 more lowercase letters.
	max64 := "Aa1" + strings.Repeat("b", 61)
	over65 := "Aa1" + strings.Repeat("b", 62)

	tests := []struct {
		name   string
		pw     string
		reason Reason
		ok     bool
	}{
		// Accepted.
		{"min ok", "Aa345678", ReasonOK, true},
		{"symbol allowed", "Aa3!5678", ReasonOK, true},
		{"exactly 8", "Abcdef1G", ReasonOK, true},
		{"exactly 64", max64, ReasonOK, true},

		// Length.
		{"7 too short", "Abcdef1", ReasonTooShort, false},
		{"empty too short", "", ReasonTooShort, false},
		{"65 too long", over65, ReasonTooLong, false},

		// Missing class.
		{"no upper", "aa345678", ReasonNoUpper, false},
		{"no lower", "AA345678", ReasonNoLower, false},
		{"no digit", "Abcdefgh", ReasonNoDigit, false},

		// Charset.
		{"internal space", "Aa 345678", ReasonCharset, false},
		{"trailing space", "Aa345678 ", ReasonCharset, false},
		{"non-ascii cjk", "密码Aa345678", ReasonCharset, false},
		{"emoji", "Aa3456\U0001F642" + "8", ReasonCharset, false},
		{"tab control", "Aa3\t5678", ReasonCharset, false},

		// Charset wins over length/class when both fail (ordering contract).
		{"cjk only is charset not short", "密", ReasonCharset, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReason, gotOK := Validate(tt.pw)
			if gotReason != tt.reason || gotOK != tt.ok {
				t.Errorf("Validate(%q) = (%v, %v), want (%v, %v)",
					tt.pw, gotReason, gotOK, tt.reason, tt.ok)
			}
		})
	}
}

func TestReasonString(t *testing.T) {
	want := map[Reason]string{
		ReasonOK:       "ok",
		ReasonCharset:  "charset",
		ReasonTooShort: "too_short",
		ReasonTooLong:  "too_long",
		ReasonNoUpper:  "no_upper",
		ReasonNoLower:  "no_lower",
		ReasonNoDigit:  "no_digit",
	}
	for r, s := range want {
		if got := r.String(); got != s {
			t.Errorf("Reason(%d).String() = %q, want %q", r, got, s)
		}
	}
}
