# Feature Specification: CI/CD Build & Release

**Feature Branch**: `014-ci-cd-build-and-release`

**Created**: 2026-06-06

**Status**: Draft

**Input**: User description: "Automate building the server .deb and the Windows client installer with GitHub Actions; publish the built artifacts to a GitHub Release; both the Windows and Ubuntu build environments install all their dependencies."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - One tag produces a complete draft release (Priority: P1)

The maintainer finishes a version, pushes a version tag (e.g. `v0.1.0`), and walks away. The
hosted CI builds the Linux server package and the Windows client installer from scratch —
installing every build dependency on its own runners — and assembles a **draft** GitHub Release
that already has every download attached, named with the version. The maintainer reviews the
draft and clicks publish.

**Why this priority**: This is the feature. Today every release is a manual, error-prone
sequence (build the server on Linux, build the client on Windows with a hand-set toolchain,
package the installer, upload files, keep versions in sync). Automating the tag → artifacts →
draft-release path is the whole point; everything else refines it.

**Independent Test**: Push a `v0.1.0` tag to a clean checkout and confirm a draft Release
appears containing the server package, the Windows installer, a portable client archive, and a
checksums file — all carrying `0.1.0` in their names — with no manual build steps.

**Acceptance Scenarios**:

1. **Given** the repository at a commit, **When** a `vX.Y.Z` tag is pushed, **Then** the
   pipeline builds the server package and the Windows client installer without any manual
   intervention.
2. **Given** a successful build, **When** the pipeline finishes, **Then** a **draft** GitHub
   Release exists with the server package, the Windows installer, a portable client archive,
   and a checksums file attached.
3. **Given** the produced artifacts, **When** the maintainer inspects them, **Then** every
   artifact filename contains the exact version taken from the tag.
4. **Given** the draft release, **When** the maintainer is ready, **Then** they publish it
   manually (the pipeline never auto-publishes).

---

### User Story 2 - A failing build or test never ships (Priority: P2)

A maintainer tags a commit that, unbeknownst to them, breaks a test or fails to build on one
platform. The pipeline must stop: no release, no half-set of artifacts. The maintainer sees the
failure and fixes it before any download is ever exposed.

**Why this priority**: A release pipeline that publishes unvalidated or partially-built
artifacts is worse than manual releasing. The test gate is what makes automation trustworthy.

**Independent Test**: Push a tag from a commit with a deliberately failing test and confirm no
Release is created and no artifacts are produced; the failure is reported on the run.

**Acceptance Scenarios**:

1. **Given** a tag on a commit whose test suite fails, **When** the pipeline runs, **Then** no
   Release is created and no artifacts are attached anywhere.
2. **Given** a tag where one platform's build fails, **When** the pipeline runs, **Then** no
   draft Release is produced from a partial set of artifacts.
3. **Given** the bundled virtual-network driver does not match its pinned checksum, **When** the
   Windows build runs, **Then** the build fails rather than shipping an unexpected binary.

---

### User Story 3 - Everyday changes keep the main line green (Priority: P3)

Outside of releases, every push and pull request runs the test suite automatically, so the main
branch is known-good at all times and a regression is caught on the change that introduced it —
not weeks later when someone cuts a tag.

**Why this priority**: Continuous validation is valuable on its own and makes the release gate
(US2) almost always pass on the first try. Lower priority than the release path because it does
not, by itself, ship anything.

**Independent Test**: Open a pull request containing a failing test and confirm the checks run
and report the failure on the PR; open one that passes and confirm the checks go green.

**Acceptance Scenarios**:

1. **Given** a pushed branch or an opened pull request, **When** CI runs, **Then** the lint and
   test suite execute and their result is reported on the change.
2. **Given** a pull request that breaks a test, **When** CI runs, **Then** the failure is
   clearly visible on the pull request.

---

### Edge Cases

- **Pre-release tag**: a tag with a pre-release suffix (e.g. `v1.2.3-rc1`) produces a Release
  flagged as a pre-release, so it is not surfaced as the repository's "Latest" release.
- **Partial failure**: if one build job fails after another succeeds, the release is not
  assembled from the incomplete set.
- **Dependency fetch failure**: if a build dependency (toolchain, packaging tool, or the
  virtual-network driver) cannot be installed or fails verification, the affected build fails
  loudly instead of producing a degraded artifact.
- **Re-running a tag**: re-triggering the same tag does not silently overwrite an
  already-published release without the maintainer's intent (drafts are reviewed before
  publishing).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: On a pushed version tag (`vX.Y.Z`), the system MUST automatically build the Linux
  server package and the Windows client installer, with each build environment installing all of
  its own dependencies, and with no manual build steps.
- **FR-002**: The release version MUST be derived from the tag as a single source of truth and
  appear both in the artifacts' embedded version information and in their filenames.
- **FR-003**: Before any artifact is published, the system MUST run the project's lint checks and
  full automated test suite — including the tests that exercise real system dependencies — and
  MUST NOT produce a release if any of them fail.
- **FR-004**: The system MUST attach the built artifacts to a GitHub Release created as a
  **draft** (never auto-published), with automatically generated release notes.
- **FR-005**: Each release MUST include the server package, the Windows client installer, a
  portable client archive (the client plus its bundled driver), and a checksums file covering
  every attached asset.
- **FR-006**: Every release artifact filename MUST contain the exact version derived from the
  tag.
- **FR-007**: A tag carrying a pre-release suffix MUST result in a Release flagged as a
  pre-release (not surfaced as "Latest").
- **FR-008**: The Windows build MUST bundle a pinned version of the virtual-network driver and
  verify it against a known checksum; a mismatch MUST fail the build.
- **FR-009**: On every push and pull request to the main line, the system MUST run the lint
  checks and test suite independently of the release flow.
- **FR-010**: The release MUST be created using the CI system's built-in, automatically-scoped
  credentials; it MUST NOT require a manually-provisioned long-lived access token.
- **FR-011**: Builds MUST target the amd64 architecture for both the server package and the
  Windows client.
- **FR-012**: Artifacts are NOT code-signed in this version; the release notes MUST tell users
  about the resulting operating-system warning and how to verify artifact integrity using the
  checksums file.

### Key Entities

This feature introduces no new application data. Its durable outputs are the **release** and its
**artifacts** (server package, Windows installer, portable archive, checksums file), described in
the requirements above.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Pushing a `vX.Y.Z` tag yields a draft Release containing all four asset types,
  each named with the version, with zero manual build steps and within 20 minutes of the push.
- **SC-002**: A tag pushed from a commit whose tests fail results in no Release and no published
  artifacts.
- **SC-003**: 100% of release artifact filenames contain the exact version from the tag.
- **SC-004**: A pre-release-tagged version is marked as a pre-release and is not shown as the
  repository's "Latest" release.
- **SC-005**: Every push and pull request runs the test suite, and a failing test is visibly
  reported on the change.
- **SC-006**: The driver bundled into the Windows installer matches its pinned checksum on 100%
  of successful builds; a mismatched driver causes a build failure.
- **SC-007**: Every release includes a checksums file with which a user can verify each attached
  asset.

## Assumptions

- The repository is hosted on GitHub (public) with Actions enabled, and hosted Linux and Windows
  runners are available.
- The Linux runner can run the project's privileged/real-dependency tests (it supports the
  unprivileged isolation the test suite uses).
- Releases are created as drafts for a human to review and publish; the pipeline never
  auto-publishes.
- Builds target amd64 only.
- This builds on the existing packaging (the server `.deb` recipe and the Windows installer
  script) and does not change the server or client application behavior.

## Out of Scope

- Code signing (Windows Authenticode, `.deb`/GPG signing).
- arm64 builds for either platform.
- Publishing to distribution channels (apt repositories, winget, other package managers).
- Auto-publishing releases (they are always left as drafts).
- Automated Windows GUI / elevation / adapter acceptance testing (validated manually; the CI
  Windows job only builds).
