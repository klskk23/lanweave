# Contract: Release & Artifacts

**Feature**: 014-ci-cd-build-and-release | **Date**: 2026-06-06

The "interface" this feature exposes is a **GitHub Release** and its attached artifacts, plus the
two workflow triggers. No RPC/HTTP surface.

## Trigger contract

| Workflow | Trigger | Effect |
|----------|---------|--------|
| `release.yml` | push of a tag matching `v*` | run gate → build → draft Release for that version |
| `ci.yml` | `push` and `pull_request` (main line) | run lint + test suite; report status on the change |

## Release contract (what a published-from-draft Release guarantees)

For a tag `vX.Y.Z`:

1. **Exists only if green.** The Release/artifacts are created only when the test gate and both
   build jobs succeeded. A failing gate or build yields no Release and no partial artifacts.
   (FR-003, US2, SC-002)
2. **Draft.** The Release is created as a draft; a human publishes it. The pipeline never
   auto-publishes. (FR-004)
3. **Assets.** The Release has exactly these attached, each containing `X.Y.Z` in its name:
   - `lanweave_X.Y.Z_amd64.deb`
   - `lanweave-client-X.Y.Z-setup.exe`
   - `lanweave-client-X.Y.Z-windows-amd64.zip`
   - `SHA256SUMS` (covers all of the above)
   (FR-005, FR-006, SC-003, SC-007)
4. **Notes.** Release notes are auto-generated; they include the unsigned-artifact / SmartScreen
   advisory and how to verify with `SHA256SUMS`. (FR-012)
5. **Prerelease flag.** If the tag has a pre-release suffix (e.g. `v1.2.3-rc1`), the Release is
   marked `prerelease` and is not surfaced as "Latest". (FR-007, SC-004)

## Integrity contract

- `SHA256SUMS` lists the SHA-256 of every other asset; `sha256sum -c SHA256SUMS` passes against
  the downloaded files.
- The `wintun.dll` inside the installer and the portable zip is exactly the pinned 0.14.1 build
  (verified by checksum during the build; a mismatch fails the build, so no Release). (FR-008,
  SC-006)

## Permissions / auth contract

- The release job runs with `permissions: contents: write` and authenticates with the built-in
  `GITHUB_TOKEN`. No personal access token or other long-lived secret is required. (FR-010)

## Non-goals

- No code signing (artifacts are unsigned; advisory in notes + DESIGN.md §11).
- No arm64 artifacts.
- No publishing to apt/winget/other channels.
- No auto-publish (always draft).
