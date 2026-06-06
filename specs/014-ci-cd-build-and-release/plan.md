# Implementation Plan: CI/CD Build & Release

**Branch**: `014-ci-cd-build-and-release` | **Date**: 2026-06-06 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/014-ci-cd-build-and-release/spec.md`

## Summary

Two GitHub Actions workflows. `release.yml` runs on a `v*` tag: a Linux **test gate**
(lint + `unshare -rUn go test ./...` + the packaging test) must pass; then `build-deb`
(`make deb` on Ubuntu) and `build-windows` (native `windows-latest`: Go + mingw + NSIS, fetch
and SHA256-verify `wintun` 0.14.1, `go build -tags gui`, `makensis`, portable zip) run in
parallel; finally a `release` job gathers all artifacts, writes `SHA256SUMS`, and creates a
**draft** GitHub Release (auto notes, `prerelease` when the tag has a pre-release suffix) using
the built-in `GITHUB_TOKEN` with `contents: write`. `ci.yml` runs lint + the same test suite on
every push/PR so `main` stays green. The version is the tag minus its leading `v`, threaded into
`make`'s `VERSION` (→ nfpm + `-ldflags`) and into the artifact filenames. No application code
changes; the existing `make deb` recipe and the NSIS script are reused.

## Technical Context

**Language/Version**: Go 1.26 (existing build); GitHub Actions workflow YAML; minimal POSIX shell
glue in workflow steps. No new application code.

**Primary Dependencies (CI-provisioned, pinned)**:
- Ubuntu runner: `actions/setup-go`, `nfpm` (pinned version), `fakeroot` (apt), `dpkg-deb`
  (preinstalled), `staticcheck` (for lint), `unshare` (preinstalled, rootless userns).
- Windows runner (`windows-latest`): `actions/setup-go`, a cgo-capable `gcc` (MSYS2/mingw on the
  image), NSIS (`choco install nsis`), `wintun` 0.14.1 (downloaded + SHA256-verified).
- Release: `softprops/action-gh-release` (or `gh release` CLI), `actions/upload-artifact` /
  `download-artifact`.

**Storage**: N/A. Durable outputs are the GitHub Release and its attached artifacts.

**Testing**:
- The pipeline *runs* the project test suite as the gate (`unshare -rUn go test ./...` + packaging
  test); that is the existing, real-dependency suite (no mocks).
- The workflows themselves are validated by **running them on GitHub** (push a real/test tag for
  `release.yml`; open a PR for `ci.yml`) — a CI/CD pipeline's behavior cannot be exercised by the
  local headless suite. Documented Principle II exception (below). Custom logic is kept minimal
  (version = `${GITHUB_REF_NAME#v}`, integrity via `sha256sum -c`) so there is little bespoke code
  to unit-test; `actionlint` may statically check the YAML.

**Target Platform**: GitHub-hosted runners — `ubuntu-latest` (server package + gate + release)
and `windows-latest` (client). Build outputs target amd64.

**Project Type**: CI/CD configuration for an existing Go monorepo (server + Windows client).

**Performance Goals**: A release run completes within ~20 minutes (SC-001). Cache Go modules to
keep it there.

**Constraints**: Single version source (the tag); release is always a **draft**; secrets limited
to the auto `GITHUB_TOKEN` (no manually-provisioned PAT); pinned action/tool/driver versions;
amd64-only.

**Scale/Scope**: Two workflow files plus small step-level shell; reuse `make build`/`make deb`,
`packaging/nfpm.yaml`, and `packaging/windows/lanweave-client.nsi` unchanged (installer renamed
to a versioned filename in the workflow).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Code Quality** — PASS. No application code; the workflows are small and single-purpose
  (`release.yml`, `ci.yml`). Logic kept to one-liners (tag→version, checksum verify) to avoid
  bespoke, hard-to-review scripting. Action/tool/driver versions pinned (reversible, obvious).
- **II. Testing Standards (NON-NEGOTIABLE)** — PASS WITH DOCUMENTED EXCEPTION. This feature's job
  *is* to run the real test suite as a release gate (FR-003), and `ci.yml` runs it on every
  push/PR (FR-009) — so it strengthens Principle II, not bypasses it. The workflows' own behavior
  (tag → tested → artifacts → draft release; failing tests → no release) can only be exercised by
  running on GitHub Actions; there is no way to assert a hosted-CI run from the local headless
  suite. Recorded in Complexity Tracking, same class of exception as the GUI/OS manual acceptance
  in 009–013. No system boundary is mocked.
- **III. User Experience Consistency** — N/A (no end-user UI). The release-page experience is
  addressed: versioned filenames, a checksums file, generated notes, and a documented SmartScreen
  note for the unsigned installer.
- **IV. Performance Requirements** — PASS. No product runtime budget is touched; the pipeline's
  own ~20-minute target (SC-001) is met with module caching.
- **Security & Operational Discipline** —
  - *Secrets*: only the auto-scoped `GITHUB_TOKEN`; no secret is echoed; no PAT (FR-010). PASS.
  - *Supply chain*: third-party actions pinned, `nfpm` pinned, and the `wintun` driver pinned +
    SHA256-verified (FR-008). PASS.
  - *Accepted risk — unsigned artifacts*: shipping unsigned binaries is a project-wide security
    decision; per the constitution that belongs in **DESIGN.md §11 (已知安全风险)**. This plan
    therefore adds a §11 entry ("release artifacts are unsigned; users get an OS publisher warning
    and verify integrity via `SHA256SUMS`"). Tracked as a task; flagged in Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/014-ci-cd-build-and-release/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output (no data entities; job DAG + artifact set)
├── quickstart.md        # Phase 1 output (how to validate on GitHub)
├── contracts/
│   └── release-artifacts.md   # The release/artifact contract
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # /speckit-tasks output (NOT created here)
```

### Source Code (repository root)

```text
.github/
└── workflows/
    ├── ci.yml           # push/PR: lint + unshare -rUn go test ./...
    └── release.yml      # v* tag: test gate → build-deb + build-windows → draft release

# Reused unchanged (logic already exists):
Makefile                              # make build / make deb, VERSION-overridable
packaging/nfpm.yaml                   # version: ${VERSION}
packaging/windows/lanweave-client.nsi # OutFile fixed; workflow renames to versioned name

# Documentation touched:
DESIGN.md                             # §11: add the "unsigned artifacts" accepted-risk entry
docs/GUIDE.en.md / docs/GUIDE.zh.md   # note that releases ship via tags (optional cross-ref)
```

**Structure Decision**: Pure CI/CD configuration under `.github/workflows/`. The two workflows
reuse the existing `make`/nfpm/NSIS recipes rather than re-implementing builds, keeping a single
source of build truth. The version flows from the tag into `make`'s `VERSION` (already the only
build version knob) so nfpm and `-ldflags` stay consistent; the Windows installer/zip are renamed
to versioned filenames in the workflow (no `.nsi` edit needed). The only non-workflow change is a
DESIGN.md §11 accepted-risk entry for unsigned artifacts.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Principle II: the workflows' own behavior (tag → gated build → draft release; failing tests → no release; SC-001..007) is validated by **running on GitHub Actions**, not by the local automated suite | A hosted-CI pipeline cannot be exercised or asserted from `go test`; its correctness only manifests when GitHub runs it on a tag/PR | Mocking GitHub Actions would assert nothing about the real pipeline (forbidden by Principle II). Bespoke logic is minimized (stdlib tag→version, `sha256sum -c`) so little is left to unit-test; the pipeline itself runs the *real* suite as its gate. Same accepted CI/OS exception class as 009–013. |
| Security: release artifacts are **unsigned** (no Authenticode / GPG) | OV/EV code-signing needs a paid cert + secret/HSM; out of proportion for a v1 public release | Self-signing is useless against SmartScreen; deferring is the pragmatic call. The risk is *registered in DESIGN.md §11* (per the constitution's accepted-risks process) with the mitigation: documented OS warning + `SHA256SUMS` integrity verification. |
