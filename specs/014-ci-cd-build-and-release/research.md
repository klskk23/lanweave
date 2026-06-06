# Research: CI/CD Build & Release

**Feature**: 014-ci-cd-build-and-release | **Date**: 2026-06-06

All decisions below were pre-aligned in a planning interview; none remain as NEEDS CLARIFICATION.

## Decision 1 — Tag-triggered release, version from the tag

- **Decision**: `release.yml` triggers only on `push` of a `v*` tag. The version is
  `${GITHUB_REF_NAME#v}` (strip the leading `v`), threaded into `make`'s `VERSION` (which already
  feeds both `nfpm` and `-ldflags -X main.version`) and into artifact filenames.
- **Rationale**: A single version source (the tag) keeps the `.deb` version, the embedded client
  version, and the filenames consistent, and maps each Release 1:1 to a tag. `make` already
  exposes `VERSION` as the only build-version knob, so no new plumbing is needed.
- **Alternatives considered**: build-and-prerelease on every `main` push (rolling) — noisier, no
  clean version; `workflow_dispatch` with a manual version input — error-prone, defeats the
  single-source goal. Rejected.

## Decision 2 — Build the Windows client on a native `windows-latest` runner

- **Decision**: Build the Fyne client on `windows-latest` (cgo `gcc` from the image's MSYS2/mingw,
  `go build -tags gui`), package with NSIS (`choco install nsis`). Do **not** cross-compile from
  Linux.
- **Rationale**: Fyne needs cgo + a real GL-capable build; cross-compiling it from Linux is
  historically fragile, and the project's whole client story (009–013) is "build on Windows".
  Windows runners are free on this public repo, so there is no cost reason to attempt the brittle
  path.
- **Alternatives considered**: `CC=x86_64-w64-mingw32-gcc GOOS=windows` cross-compile — saves a
  runner but invites GL/cgo link breakage. Rejected.

## Decision 3 — Fetch and SHA256-verify Wintun 0.14.1

- **Decision**: The Windows job downloads `https://www.wintun.net/builds/wintun-0.14.1.zip`,
  verifies its SHA256 against a pinned value, extracts `bin/amd64/wintun.dll`, and places it next
  to the built `.exe` for NSIS and the portable zip. A checksum mismatch fails the build (FR-008).
- **Rationale**: The driver is not in the repo (it is gitignored); CI must supply it. Pinning the
  version + verifying the hash prevents "whatever the URL serves that day" from being baked into a
  shipped installer.
- **Open item**: the exact SHA256 for 0.14.1 is filled into the workflow at implementation time
  (the user will confirm/adjust). Until then it is a named placeholder, not a silent skip.
- **Alternatives considered**: vendor the DLL in-repo — best reproducibility but contradicts the
  existing gitignore and puts a binary in git; download without verification — supply-chain risk.
  Rejected.

## Decision 4 — Linux test gate (real suite) blocks the build

- **Decision**: A `test` job on `ubuntu-latest` runs lint (`gofmt`/`go vet`/`staticcheck`) then
  `unshare -rUn go test ./...`. It installs `nfpm` (pinned) and `fakeroot` first so the host-gated
  packaging test actually runs (`dpkg-deb` is preinstalled). `build-deb` and `build-windows`
  `needs: test`.
- **Rationale**: Constitution Principle II is non-negotiable and forbids mocking the real
  SQLite/nftables/WireGuard. `unshare -rUn` gives the rootless user+net namespace the kernel tests
  need (without it `RequireNetAdmin` skips them and the gate is hollow).
- **Runner image + fail-closed preflight (F1 mitigation)**: GitHub's `ubuntu-latest` is now
  Ubuntu 24.04, which restricts unprivileged user namespaces via
  `kernel.apparmor_restrict_unprivileged_userns=1` and could silently make the kernel tests skip.
  The jobs that run `unshare -rUn` therefore pin **`ubuntu-22.04`** (unprivileged userns allowed by
  default) and add a **fail-closed preflight** (`unshare -rUn -- sh -c 'ip link set lo up && ip
  link'`) that aborts the job if rootless namespaces are unavailable — so the gate can never pass
  with the privileged tests silently skipped. (On 24.04 the alternative is
  `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`; pinning 22.04 is simpler and
  also satisfies supply-chain version pinning.)
- **Alternatives considered**: run only non-privileged tests (hollow gate); skip tests entirely
  (violates the constitution). Rejected.

## Decision 5 — Draft release via `GITHUB_TOKEN`, prerelease from the tag

- **Decision**: A `release` job (`needs: [build-deb, build-windows]`, `permissions: contents:
  write`) downloads all artifacts, generates `SHA256SUMS`, and creates a **draft** Release with
  auto-generated notes via `softprops/action-gh-release` (pinned). It sets `prerelease: true` when
  the tag contains a pre-release suffix (a `-` in the semver, e.g. `v1.2.3-rc1`). Auth is the
  auto-injected `GITHUB_TOKEN` — no PAT.
- **Rationale**: Creating a Release in the same repo needs only `GITHUB_TOKEN` with
  `contents: write` declared in the workflow; a PAT is unnecessary. Draft + manual publish gives
  the maintainer a review gate. Prerelease keeps RC/beta tags out of "Latest".
- **Alternatives considered**: a PAT (needless extra secret + rotation); auto-publish (no human
  review). Rejected.

## Decision 6 — Artifact set, naming, and checksums

- **Decision**: Each release carries: `lanweave_<ver>_amd64.deb` (nfpm), `lanweave-client-<ver>-
  setup.exe` (NSIS output renamed in the workflow), a portable zip
  `lanweave-client-<ver>-windows-amd64.zip` (`lanweave-client.exe` + `wintun.dll`), and a
  `SHA256SUMS` covering all of them. The NSIS `OutFile` stays fixed; the workflow renames it to
  the versioned name before upload (no `.nsi` edit).
- **Rationale**: Versioned names prevent grabbing the wrong build; the portable zip serves users
  who skip the installer; `SHA256SUMS` is the only integrity check users have given that artifacts
  are unsigned. Renaming in the workflow keeps the `.nsi` simple.
- **Alternatives considered**: parameterize `OutFile` via `makensis -DVERSION` — also fine but
  edits the `.nsi`; rename-after is the smaller change. Either acceptable.

## Decision 7 — Plain CI on push/PR

- **Decision**: `ci.yml` runs on `push` and `pull_request` to the main line: lint + `unshare -rUn
  go test ./...` (same real suite as the gate). Optional later: a Windows compile-check job.
- **Rationale**: Keeps `main` green continuously and makes the release gate (US2) almost always
  pass first try; without it, failures surface only when a tag is cut.
- **Alternatives considered**: only the tag pipeline (late feedback). Rejected.

## Decision 8 — No code signing (registered accepted risk)

- **Decision**: Do not sign artifacts in v1. Record the accepted risk in **DESIGN.md §11** with
  the mitigation (documented SmartScreen note + `SHA256SUMS` integrity), and state it in the
  release notes.
- **Rationale**: OV/EV Authenticode needs a paid cert + secret/HSM; self-signing is useless for
  SmartScreen. The constitution requires project-wide accepted risks to live in DESIGN.md §11, so
  this plan adds that entry rather than leaving the decision implicit.
- **Alternatives considered**: buy a cert now (cost/scope); self-sign (no benefit). Deferred.

## Decision 9 — Supply-chain pinning

- **Decision**: Pin third-party action versions (tag or commit SHA), pin `nfpm`, pin `wintun`
  0.14.1 + checksum. Cache Go modules for runtime.
- **Rationale**: Reproducible, auditable builds (constitution dependency-hygiene); avoids a moving
  action silently changing release behavior.

## Resolved unknowns summary

| Topic | Resolution |
|-------|------------|
| Trigger / version | `v*` tag → version `${GITHUB_REF_NAME#v}` → `make VERSION` + filenames |
| Windows build | native `windows-latest` (mingw + NSIS), not cross-compile |
| Driver | download wintun 0.14.1 + SHA256 verify → `bin/amd64/wintun.dll` |
| Test gate | `unshare -rUn go test ./...` + lint + packaging test; blocks build |
| Release | draft, auto notes, prerelease-from-tag, `GITHUB_TOKEN` + `contents: write` |
| Artifacts | deb + installer + portable zip + `SHA256SUMS`, versioned names |
| Plain CI | `ci.yml` on push/PR: lint + real test suite |
| Signing | none in v1; accepted risk in DESIGN.md §11 + release-note advisory |
