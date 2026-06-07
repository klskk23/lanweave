// Package passwordpolicy is the single source of truth for whether a user
// account password is acceptable. It is imported by both the server's register
// handler and the Fyne client wizard so the two can never disagree. It is pure:
// no I/O, no logging, and it never returns or echoes the password itself.
package passwordpolicy

// Length bounds, counted in bytes — which equals characters because a valid
// password is ASCII-only (any non-ASCII byte fails the charset rule first).
const (
	minLen = 8
	maxLen = 64
)

// Reason identifies which rule a candidate password failed, or ReasonOK.
type Reason int

const (
	ReasonOK       Reason = iota
	ReasonCharset         // contains a space or a non-ASCII / control character
	ReasonTooShort        // fewer than minLen characters
	ReasonTooLong         // more than maxLen characters
	ReasonNoUpper         // no ASCII A-Z
	ReasonNoLower         // no ASCII a-z
	ReasonNoDigit         // no 0-9
)

// String returns a stable, lowercase, machine-friendly token for the reason. It
// is used as the client i18n key suffix and in test assertions; it is NOT a
// user-facing message.
func (r Reason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonCharset:
		return "charset"
	case ReasonTooShort:
		return "too_short"
	case ReasonTooLong:
		return "too_long"
	case ReasonNoUpper:
		return "no_upper"
	case ReasonNoLower:
		return "no_lower"
	case ReasonNoDigit:
		return "no_digit"
	default:
		return "unknown"
	}
}

// Validate reports whether pw satisfies the account-password policy. On failure
// it returns the first failing Reason in a fixed evaluation order — charset,
// then length, then required character classes — so the same input always yields
// the same verdict. Charset is checked first so that a disallowed character
// (e.g. a pasted full-width digit) does not masquerade as a missing-digit error.
func Validate(pw string) (Reason, bool) {
	var hasUpper, hasLower, hasDigit bool
	for i := 0; i < len(pw); i++ {
		b := pw[i]
		if b < 0x21 || b > 0x7e { // outside ASCII printable, or a space
			return ReasonCharset, false
		}
		switch {
		case b >= 'A' && b <= 'Z':
			hasUpper = true
		case b >= 'a' && b <= 'z':
			hasLower = true
		case b >= '0' && b <= '9':
			hasDigit = true
		}
	}
	switch {
	case len(pw) < minLen:
		return ReasonTooShort, false
	case len(pw) > maxLen:
		return ReasonTooLong, false
	case !hasUpper:
		return ReasonNoUpper, false
	case !hasLower:
		return ReasonNoLower, false
	case !hasDigit:
		return ReasonNoDigit, false
	}
	return ReasonOK, true
}
