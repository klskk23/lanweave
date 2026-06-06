# Quickstart: CI/CD Build & Release

**Feature**: 014-ci-cd-build-and-release | **Date**: 2026-06-06

How to validate the pipeline. Steps A–B are local sanity checks; C–E exercise the real workflows
on GitHub (the only place a hosted-CI pipeline can be exercised — the documented Principle II
exception).

## A. Local sanity (dev host)

```sh
# the release build commands the workflow will call, with a tag-style version:
make VERSION=0.1.0 deb        # → dist/lanweave_0.1.0_amd64.deb
make VERSION=0.1.0 build      # → ./lanweaved with version embedded

# the gate command (real suite; needs nfpm + fakeroot for the packaging test):
unshare -rUn go test ./...
```

Optional: `actionlint .github/workflows/*.yml` to statically check the workflow YAML.

## B. Inspect the workflows

- `.github/workflows/ci.yml`: triggers on push/PR; runs lint + `unshare -rUn go test ./...`.
- `.github/workflows/release.yml`: triggers on `v*`; `test` gate → `build-deb` + `build-windows`
  → `release` (draft). Confirm `permissions: contents: write` on the release job, pinned action
  versions, and the wintun SHA256.

## C. US1 — tag → draft release (happy path)

```sh
git tag v0.1.0
git push origin v0.1.0
```

Watch the Actions run. Expect: `test` green → `build-deb` + `build-windows` green → `release`
creates a **draft** Release for `v0.1.0` with:
`lanweave_0.1.0_amd64.deb`, `lanweave-client-0.1.0-setup.exe`,
`lanweave-client-0.1.0-windows-amd64.zip`, `SHA256SUMS`. (SC-001, SC-003, SC-007)

Verify integrity locally after downloading:

```sh
sha256sum -c SHA256SUMS
```

Then publish the draft from the Releases UI (the pipeline does not auto-publish).

## D. US2 — failing tests block the release

1. On a branch, introduce a deliberately failing test; tag it (e.g. `v0.0.1-broken`) and push the
   tag.
2. Expect: the `test` job fails, `build-*` and `release` never run, **no Release is created**.
   (SC-002) Remove the bad tag afterward.

## E. US3 — CI on push/PR

1. Open a pull request.
2. Expect: `ci.yml` runs lint + the test suite and reports status on the PR; a failing test shows
   up as a failed check. (SC-005)

## F. Pre-release tag (edge case)

```sh
git tag v0.1.0-rc1 && git push origin v0.1.0-rc1
```

Expect: the draft Release is flagged **pre-release** and is not shown as "Latest". (SC-004)

## Pass criteria

- A–B succeed locally / statically.
- C produces a complete, versioned, checksummed draft Release with no manual build steps.
- D produces no Release.
- E reports test status on the PR.
- F marks the release as a pre-release.
