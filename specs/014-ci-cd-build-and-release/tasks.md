---

description: "Task list for 014-ci-cd-build-and-release"
---

# Tasks: CI/CD Build & Release

**Input**: Design documents from `specs/014-ci-cd-build-and-release/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/release-artifacts.md, quickstart.md

**Tests**: This feature adds no application code, so there is no new automated unit/integration
test to write — instead the pipeline *runs* the existing real-dependency suite as its gate. The
workflows' own behavior (tag → gated build → draft release; failing tests → no release) can only
be exercised by running on GitHub Actions; those are the manual acceptance tasks below
(documented Principle II exception in plan.md, same class as 009–013). Local static checks
(`make VERSION=… deb`, `actionlint`) substitute where possible.

**Organization**: Tasks are grouped by user story. US1 = the tag→draft-release pipeline (MVP),
US2 = the test/build gate that blocks bad releases, US3 = the push/PR CI workflow.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- Exact file paths included in each task

## Path Conventions

New workflows under `.github/workflows/`; risk note in `DESIGN.md`; doc cross-refs in
`docs/GUIDE.en.md` / `docs/GUIDE.zh.md`. Build recipes (`Makefile`, `packaging/nfpm.yaml`,
`packaging/windows/lanweave-client.nsi`) are reused unchanged.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create the workflow location

- [X] T001 Create the `.github/workflows/` directory to hold `ci.yml` and `release.yml`.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Lock the version flow and the pinned dependency versions both workflows reuse

**⚠️ CRITICAL**: The build jobs depend on these before they can be written.

- [X] T002 Confirm the version flow locally and record the tag→version rule: `make VERSION=0.1.0 deb` produces `dist/lanweave_0.1.0_amd64.deb` and `make VERSION=0.1.0 build` embeds `0.1.0`; both workflows will derive the version as `${GITHUB_REF_NAME#v}`. (Verification against `Makefile`; no Makefile change expected.)
- [X] T003 Establish the pinned versions reused by both workflows and record them (as workflow `env`/comments): Go toolchain version for `actions/setup-go` (1.26, matching `go.mod`), `nfpm` version, third-party action tags/SHAs (`actions/checkout`, `actions/setup-go`, `actions/upload-artifact`, `actions/download-artifact`, `softprops/action-gh-release`), the **wintun 0.14.1 SHA256**, and the **runner image** (`ubuntu-22.04` for jobs that run `unshare -rUn`; see T004/T011).

**Checkpoint**: version derivation and pins are settled.

---

## Phase 3: User Story 1 - One tag produces a complete draft release (Priority: P1) 🎯 MVP

**Goal**: Pushing a `vX.Y.Z` tag builds everything and assembles a draft GitHub Release with the
four versioned, checksummed assets — no manual build steps.

**Independent Test**: quickstart §C — push `v0.1.0`, see a draft Release with
`lanweave_0.1.0_amd64.deb`, `lanweave-client-0.1.0-setup.exe`,
`lanweave-client-0.1.0-windows-amd64.zip`, `SHA256SUMS`; `sha256sum -c` passes.

### Implementation for User Story 1

- [X] T004 [US1] Create `.github/workflows/release.yml` triggered on `push` tags `v*`; add the `test` gate job on **`ubuntu-22.04`** (pinned; allows unprivileged user namespaces so `unshare -rUn` works without the 24.04 AppArmor restriction): checkout, `actions/setup-go`, install `nfpm` + `fakeroot` + `staticcheck`; add a **fail-closed netns preflight** that aborts the job if rootless namespaces are unavailable (`unshare -rUn -- sh -c 'ip link set lo up && ip link' || { echo '::error::unprivileged user+net namespace unavailable'; exit 1; }`) so the gate can never pass with the privileged tests silently skipped; run lint (`gofmt -l` check, `go vet ./...`, `staticcheck ./...`), then `unshare -rUn go test ./...`.
- [X] T005 [US1] In `.github/workflows/release.yml`, add `build-deb` (`ubuntu-latest`, `needs: test`): set `VERSION=${GITHUB_REF_NAME#v}`, install pinned `nfpm`, run `make VERSION=$VERSION deb`, upload `dist/lanweave_${VERSION}_amd64.deb` via `actions/upload-artifact`.
- [X] T006 [US1] In `.github/workflows/release.yml`, add `build-windows` (`windows-latest`, `needs: test`): `actions/setup-go` + ensure a cgo `gcc` (mingw), `choco install nsis`, download `wintun-0.14.1.zip` and verify its SHA256, extract `bin/amd64/wintun.dll` next to the build, `go build -tags gui -ldflags "-H windowsgui -X main.version=$VERSION" -o lanweave-client.exe ./cmd/lanweave-client`, run `makensis packaging/windows/lanweave-client.nsi`, rename output to `lanweave-client-${VERSION}-setup.exe`, zip `lanweave-client.exe`+`wintun.dll` to `lanweave-client-${VERSION}-windows-amd64.zip`, upload both via `actions/upload-artifact`.
- [X] T007 [US1] In `.github/workflows/release.yml`, add the `release` job (`ubuntu-latest`, `needs: [build-deb, build-windows]`, `permissions: contents: write`): `actions/download-artifact` all assets, generate `SHA256SUMS` (`sha256sum * > SHA256SUMS`), create a **draft** Release with auto-generated notes via pinned `softprops/action-gh-release` using `GITHUB_TOKEN`, attaching all four assets; set `prerelease: true` when `${GITHUB_REF_NAME}` contains a `-` (pre-release suffix).
- [ ] T008 [US1] Manual acceptance on GitHub (quickstart §C): push `v0.1.0` → draft Release appears with the four versioned assets; `sha256sum -c SHA256SUMS` passes; no manual build steps. (SC-001, SC-003, SC-007)

**Checkpoint**: MVP — a tag yields a complete draft Release.

---

## Phase 4: User Story 2 - A failing build or test never ships (Priority: P2)

**Goal**: A failing gate, a failing build, or a tampered driver produces no Release and no partial
artifact set.

**Independent Test**: quickstart §D — tag a commit with a failing test → no Release is created.

### Implementation for User Story 2

- [X] T009 [US2] Verify the gate wiring in `.github/workflows/release.yml`: `build-deb` and `build-windows` both `needs: test`, and `release` `needs: [build-deb, build-windows]`, so a failed gate or either build yields no Release and no partial set; confirm the wintun SHA256 step (T006) fails the job on mismatch (FR-008).
- [ ] T010 [US2] Manual acceptance on GitHub (quickstart §D): push a tag from a commit with a deliberately failing test → no Release and no artifacts; remove the bad tag afterward. (SC-002, SC-006)

**Checkpoint**: bad inputs cannot produce a release.

---

## Phase 5: User Story 3 - Everyday changes keep the main line green (Priority: P3)

**Goal**: Every push/PR runs lint + the real test suite and reports status on the change.

**Independent Test**: quickstart §E — open a PR; CI runs and reports; a failing test = failed check.

### Implementation for User Story 3

- [X] T011 [US3] Create `.github/workflows/ci.yml` triggered on `push` and `pull_request`: a job on **`ubuntu-22.04`** (pinned, for `unshare -rUn`) that checks out, `actions/setup-go` (with Go module caching), installs `staticcheck` (+ `nfpm`/`fakeroot` so the packaging test runs), runs the **same fail-closed netns preflight as T004**, runs lint, then `unshare -rUn go test ./...`.
- [ ] T012 [US3] Manual acceptance on GitHub (quickstart §E): open a PR → `ci.yml` runs lint + tests and reports on the PR; a failing test shows as a failed check. (SC-005)

**Checkpoint**: main line is continuously validated.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Constitution-required risk registration, user-facing advisories, and validation

- [X] T013 [P] Register the accepted risk in `DESIGN.md` §11 (已知安全风险): add a row "release artifacts are unsigned" with mitigation "documented SmartScreen/OS warning + `SHA256SUMS` integrity verification" (constitution accepted-risks process; FR-012).
- [X] T014 [P] In the `release` job body/notes (`.github/workflows/release.yml`), include the advisory: artifacts are unsigned (expect a SmartScreen/publisher warning) and how to verify integrity with `SHA256SUMS` (FR-012).
- [ ] T015 Manual acceptance on GitHub (quickstart §F): push `v0.1.0-rc1` → the draft Release is flagged **pre-release** and is not surfaced as "Latest". (SC-004)
- [X] T016 [P] Statically validate the workflows: `actionlint .github/workflows/*.yml` and confirm `make VERSION=0.1.0 deb` works locally (quickstart §A/B).
- [X] T017 [P] Cross-reference in `docs/GUIDE.en.md` and `docs/GUIDE.zh.md`: note that official releases are produced by pushing a `v*` tag (and are reviewed as drafts before publishing).

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none — start immediately.
- **Foundational (Phase 2)**: after Setup; T002/T003 inform the build jobs.
- **US1 (Phase 3)**: after Foundational. T004 (file + gate) before T005/T006 (jobs that `needs: test`) before T007 (release job that `needs` both builds).
- **US2 (Phase 4)**: after US1 (verifies edges added across T004–T007).
- **US3 (Phase 5)**: independent of US1/US2 (separate file `ci.yml`); after Foundational.
- **Polish (Phase 6)**: after the workflows exist; T013/T017 are docs (independent), T014 edits the release job, T015/T016 validate.

### Within Each Story

- US1: T004 → T005 / T006 (parallelizable across the two build jobs, but same file `release.yml` so edit sequentially) → T007 → T008 (manual).
- US2: T009 (verify) → T010 (manual).
- US3: T011 → T012 (manual).

### Parallel Opportunities

- T013, T017 (docs) and T016 (static check) are different files → `[P]`.
- US3 (`ci.yml`) can be built in parallel with US1 (`release.yml`) — different files.
- The manual GitHub acceptances (T008, T010, T012, T015) run together once the workflows are pushed.

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1 Setup → Phase 2 Foundational.
2. Phase 3 US1: write `release.yml` (gate + build-deb + build-windows + release).
3. **STOP and VALIDATE**: push `v0.1.0`, confirm the draft Release (quickstart §C).

### Incremental Delivery

1. Setup + Foundational → version flow + pins ready.
2. US1 → tag yields a draft Release (MVP).
3. US2 → confirm the gate blocks bad inputs.
4. US3 → add `ci.yml` for continuous validation.
5. Polish → §11 risk entry, release-note advisory, prerelease/static/doc validation.

---

## Notes

- No application code changes; build recipes (`make`/nfpm/NSIS) are reused unchanged.
- The pipeline *runs* the real test suite (no mocks); workflow behavior is validated by running on GitHub (documented Principle II exception).
- Pin all action/tool/driver versions (supply-chain hygiene); fill the real wintun 0.14.1 SHA256 in T003/T006.
- F1 mitigation: the jobs running `unshare -rUn` pin `ubuntu-22.04` (avoids the 24.04 unprivileged-userns AppArmor restriction) and run a fail-closed preflight so the gate cannot pass with the privileged tests silently skipped (T004/T011).
- Commit after each logical group; the release job uses only the auto `GITHUB_TOKEN` (no PAT).
