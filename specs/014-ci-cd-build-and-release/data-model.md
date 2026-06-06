# Data Model: CI/CD Build & Release

**Feature**: 014-ci-cd-build-and-release | **Date**: 2026-06-06

No application data entities. The "model" for a CI/CD feature is the **pipeline job DAG**, the
**version derivation**, and the **release artifact set** — the durable contracts the pipeline
establishes.

## Pipeline job DAG (`release.yml`, on `v*` tag)

```text
push tag v*
   └─ test (ubuntu-latest)                         ← gate; must pass
        • lint: gofmt + go vet + staticcheck
        • install nfpm (pinned) + fakeroot
        • unshare -rUn go test ./...   (real SQLite/nftables/WG + packaging test)
        ├─ build-deb (ubuntu-latest, needs: test)
        │     • make deb  (VERSION=<ver>)  → upload-artifact: dist/*.deb
        └─ build-windows (windows-latest, needs: test)
              • setup-go + mingw gcc + choco install nsis
              • download wintun 0.14.1 + verify SHA256 → bin/amd64/wintun.dll
              • go build -tags gui  (version=<ver>)  → lanweave-client.exe
              • makensis → rename to lanweave-client-<ver>-setup.exe
              • zip exe+dll → lanweave-client-<ver>-windows-amd64.zip
              • upload-artifact: installer + zip
              └─ release (ubuntu-latest, needs: [build-deb, build-windows],
                          permissions: contents: write)
                    • download all artifacts
                    • generate SHA256SUMS over every asset
                    • create DRAFT Release (auto notes; prerelease if tag has a pre-release suffix)
```

## Version derivation

| Field | Source | Rule |
|-------|--------|------|
| `version` | tag `GITHUB_REF_NAME` | strip leading `v` → `X.Y.Z[-suffix]` |
| `prerelease` | tag | true when the version contains a pre-release suffix (a `-`, e.g. `-rc1`) |
| nfpm package version | `make VERSION=<version>` | flows to `packaging/nfpm.yaml` `version: ${VERSION}` |
| client embedded version | `make VERSION=<version>` | flows to `-ldflags -X main.version=<version>` |
| artifact filenames | `<version>` | embedded into each asset name |

## Release artifact set (one per Release)

| Asset | Producer | Name |
|-------|----------|------|
| Server package | `make deb` (nfpm) | `lanweave_<version>_amd64.deb` |
| Windows installer | NSIS, renamed in workflow | `lanweave-client-<version>-setup.exe` |
| Portable client | zip of exe + driver | `lanweave-client-<version>-windows-amd64.zip` |
| Checksums | `sha256sum` in release job | `SHA256SUMS` |

## CI workflow (`ci.yml`, on push / pull_request)

| Step | Action |
|------|--------|
| lint | gofmt check + go vet + staticcheck |
| test | `unshare -rUn go test ./...` (same real suite as the gate) |

## Invariants

- A Release is produced **only** if the `test` gate passed (FR-003, SC-002).
- A Release is produced **only** if both build jobs succeeded (no partial set; edge case).
- Every artifact filename contains the exact tag version (FR-006, SC-003).
- The bundled `wintun.dll` matches the pinned SHA256 or the build fails (FR-008, SC-006).
- The Release is always a **draft**; the pipeline never auto-publishes (FR-004).
- Pre-release-suffixed tags yield `prerelease: true` (FR-007, SC-004).
- Auth is the auto `GITHUB_TOKEN` with `contents: write`; no PAT (FR-010).
- Artifacts target amd64 only (FR-011).
