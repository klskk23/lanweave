# Data Model: Password Complexity

This feature **adds no persistent state** — no table, no column, no migration. It
constrains a transient input value (the account password) at the moment it is set.
The "model" here is the in-memory policy contract shared by client and server.

## Entity: Password Policy (logic, not storage)

The single rule set, implemented in `pkg/passwordpolicy`.

| Rule | Definition |
|------|------------|
| Length | 8 ≤ len(password) ≤ 64, counted in characters (= bytes; ASCII-only) |
| Uppercase | ≥ 1 character in `A`–`Z` |
| Lowercase | ≥ 1 character in `a`–`z` |
| Digit | ≥ 1 character in `0`–`9` |
| Character set | every character in `0x21`–`0x7E` (ASCII printable, **excluding** space `0x20`) |
| Symbols | ASCII punctuation/symbols allowed, not required |

A password is **valid** iff all rules hold.

## Validation Reason (typed enum)

`Validate` returns one reason describing the **first** failing rule in a fixed
evaluation order, plus an ok flag. The order is deterministic so the same input always
yields the same reason (SC-005, testability).

| Reason | Meaning | Evaluation order |
|--------|---------|------------------|
| `ReasonOK` | all rules satisfied | — (returned with ok = true) |
| `ReasonCharset` | contains a space or a non-ASCII / control character | 1 |
| `ReasonTooShort` | fewer than 8 characters | 2 |
| `ReasonTooLong` | more than 64 characters | 3 |
| `ReasonNoUpper` | no ASCII uppercase letter | 4 |
| `ReasonNoLower` | no ASCII lowercase letter | 5 |
| `ReasonNoDigit` | no digit | 6 |

**Order rationale**: charset is checked first because a disallowed character (e.g. a
pasted full-width digit) otherwise produces a misleading "missing digit"; length next
(cheap, most common); class checks last. The order is a contract — both the server
message mapping and the client i18n mapping rely on it, and the unit tests pin it.

## Relationship to existing model

- `users.password_hash` (argon2id) is unchanged: the policy runs **before** hashing
  on the register path; storage and verification are untouched.
- No relationship to `invites`, `nodes`, `zones`, or `refresh_tokens`.

## State transitions

None. Validation is a pure, stateless function of the candidate password string.
