#!/usr/bin/env bash
# Generate every raster icon form the lanweave Windows client needs, all from the single
# vector source packaging/icon.svg:
#   - packaging/icon.ico                           16/32/48/256, for the EXE resource (windres) + NSIS
#   - internal/client/ui/icon.png                  256x256, embedded as the Fyne window icon
#   - cmd/lanweave-client/resources_windows.syso   COFF object the Go linker embeds into the EXE
# Requires: rsvg-convert (librsvg), icotool (icoutils), windres (MinGW binutils).
set -euo pipefail

cd "$(dirname "$0")/../.."   # repo root

SVG="packaging/icon.svg"
ICO="packaging/icon.ico"
PNG="internal/client/ui/icon.png"
RC="packaging/windows/icon.rc"
SYSO="cmd/lanweave-client/resources_windows.syso"

# windres is x86_64-w64-mingw32-windres on Linux, plain windres on Windows/MinGW.
WINDRES="${WINDRES:-$(command -v x86_64-w64-mingw32-windres || command -v windres || true)}"

for tool in rsvg-convert icotool "$WINDRES"; do
	if [ -z "$tool" ] || ! command -v "$tool" >/dev/null 2>&1; then
		echo "gen-icons: required tool not found: ${tool:-windres}" >&2
		exit 1
	fi
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

for n in 16 32 48 256; do
	rsvg-convert -w "$n" -h "$n" "$SVG" -o "$tmp/icon-$n.png"
done

icotool -c -o "$ICO" "$tmp/icon-16.png" "$tmp/icon-32.png" "$tmp/icon-48.png" "$tmp/icon-256.png"
cp "$tmp/icon-256.png" "$PNG"

# The _windows filename suffix scopes this COFF object to GOOS=windows, so the headless stub
# build (GOOS=linux) ignores it; the Go linker auto-links *.syso from the main package dir.
"$WINDRES" -I . "$RC" -O coff -o "$SYSO"

echo "gen-icons: wrote $ICO, $PNG, $SYSO"
