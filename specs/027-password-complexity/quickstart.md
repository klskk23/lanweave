# Quickstart: Password Complexity

Audience: developer/operator verifying slice 027.

## The rule

Account passwords (set at registration) must be:

- **8–64 characters**
- at least one **uppercase** letter, one **lowercase** letter, one **digit** (ASCII)
- **ASCII printable only** — no spaces, no non-ASCII (Chinese/emoji/accented), no
  control characters
- ASCII symbols (`!@#…`) are allowed but not required

Examples: `Aa345678` ✅ `Aa3!5678` ✅ `aa345678` ❌ (no upper) `Abcdefgh` ❌ (no digit)
`Aa 345678` ❌ (space) `密码Aa345678` ❌ (non-ASCII).

## Behavior to verify

1. **Server rejects weak passwords (US1)**: register with `aa345678` → `400`
   `validation_error`, no account created, invite not consumed. Register with
   `Aa345678` → account created.
2. **Boundaries**: 7 chars rejected, 8 accepted, 64 accepted, 65 rejected.
3. **Client blocks submit (US2)**: in the wizard's create-account step, a
   non-compliant password prevents advancing and shows the specific failing rule,
   localized to the UI language — no server round trip.
4. **Persistent hint (US3)**: the rule description is visible beneath the password
   field before typing anything, in the UI language.
5. **Login not gated (FR-009)**: an account whose password predates this policy (or
   the bootstrap admin) still logs in — the policy is not applied at login.
6. **Out of scope**: creating/changing a zone password and the bootstrap admin
   password behave exactly as before.

## Run the tests

```console
# Pure policy unit tests (deterministic, no isolation needed):
$ go test ./pkg/passwordpolicy/...

# Register acceptance against real SQLite:
$ unshare -rUn go test ./internal/server/api/... -run Register

# Client wizard (loopback up like the existing suites):
$ unshare -rUn sh -c 'ip link set lo up; go test ./internal/client/...'

# Whole tree green after fixture updates:
$ go build ./...
$ unshare -rUn go test ./internal/server/... ./pkg/...
$ unshare -rUn sh -c 'ip link set lo up; go test ./internal/client/...'
```

## Notes

- No database migration — this slice adds no persistent state.
- Hashing remains argon2id; the policy runs before hashing and never logs the
  password.
- DESIGN.md §7 documents the policy; ROADMAP 027 is checked off at merge.
