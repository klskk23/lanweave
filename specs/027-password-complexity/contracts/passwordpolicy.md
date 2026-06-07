# Contract: `pkg/passwordpolicy`

The single shared source of truth for account-password validity. Imported by the
server `register` handler and the Fyne client wizard. Pure, stateless, no I/O, no
logging, never returns or echoes the password.

## API

```go
package passwordpolicy

// Reason identifies which rule a candidate password failed (or OK).
type Reason int

const (
    ReasonOK Reason = iota
    ReasonCharset  // contains space or non-ASCII/control character
    ReasonTooShort // < 8 characters
    ReasonTooLong  // > 64 characters
    ReasonNoUpper  // no ASCII A-Z
    ReasonNoLower  // no ASCII a-z
    ReasonNoDigit  // no 0-9
)

// String returns a stable, lowercase, machine-friendly token for the reason
// (e.g. "charset", "too_short", "no_upper"). Used for the client i18n key suffix
// and for test assertions. NOT a user-facing message.
func (r Reason) String() string

// Validate reports whether pw satisfies the account-password policy. On failure it
// returns the first failing Reason in the fixed evaluation order; on success it
// returns (ReasonOK, true).
func Validate(pw string) (Reason, bool)
```

## Behavioral guarantees

- **Bounds**: `len < 8` → `ReasonTooShort`; `len > 64` → `ReasonTooLong`. Exactly 8
  and exactly 64 are valid. Length is `len(pw)` in bytes (equals characters because a
  valid password is ASCII-only).
- **Charset first**: any byte outside `0x21`–`0x7E` (this includes the space `0x20`,
  ASCII control characters, and every non-ASCII/multibyte rune) → `ReasonCharset`,
  reported before any length or class reason.
- **Class checks**: after charset and length pass, missing uppercase → `ReasonNoUpper`,
  else missing lowercase → `ReasonNoLower`, else missing digit → `ReasonNoDigit`.
- **Determinism**: same input → same `(Reason, ok)` every time. No randomness, clock,
  or environment read.
- **Purity / security**: does not hash, does not log, does not include the password in
  the returned reason or any error.

## Examples

| Input | Result |
|-------|--------|
| `Aa345678` | `(ReasonOK, true)` |
| `Aa3!5678` | `(ReasonOK, true)` (ASCII symbol allowed) |
| (64 × valid mix incl. upper/lower/digit) | `(ReasonOK, true)` |
| `Abc12` (5) | `(ReasonTooShort, false)` |
| 65-char compliant-class string | `(ReasonTooLong, false)` |
| `aa345678` | `(ReasonNoUpper, false)` |
| `AA345678` | `(ReasonNoLower, false)` |
| `Abcdefgh` | `(ReasonNoDigit, false)` |
| `Aa 345678` (space) | `(ReasonCharset, false)` |
| `密码Aa345678` (non-ASCII) | `(ReasonCharset, false)` |
| `Aa3456🙂8` (emoji) | `(ReasonCharset, false)` |

## Test obligations (unit, table-driven)

- Every reason has at least one row, plus the OK rows above.
- Boundary rows: 7/8/64/65 length.
- A multi-violation input (e.g. `密` alone) asserts the charset-first ordering.
- `Reason.String()` returns the documented stable token for each constant.
