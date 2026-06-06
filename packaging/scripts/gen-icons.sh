#!/usr/bin/env bash
# Generate every raster icon form the lanweave Windows client needs, all from the single
# vector source packaging/icon.svg:
#   - packaging/icon.ico                           16/32/48/256, for the EXE resource (windres) + NSIS
#   - internal/client/ui/icon.png                  256x256, embedded as the Fyne window icon
#   - cmd/lanweave-client/resources_windows.syso   COFF object the Go linker embeds into the EXE
# Requires: rsvg-convert (librsvg), windres (MinGW binutils), and an ICO assembler --
# icotool (icoutils) where available, else ImageMagick (magick/convert).
set -euo pipefail

cd "$(dirname "$0")/../.."   # repo root

SVG="packaging/icon.svg"
ICO="packaging/icon.ico"
PNG="internal/client/ui/icon.png"
RC="packaging/windows/icon.rc"
SYSO="cmd/lanweave-client/resources_windows.syso"

# windres is x86_64-w64-mingw32-windres on Linux, plain windres on Windows/MinGW.
WINDRES="${WINDRES:-$(command -v x86_64-w64-mingw32-windres || command -v windres || true)}"

for tool in rsvg-convert "$WINDRES"; do
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

# Assemble the multi-size .ico. Prefer icotool (icoutils) where available (Linux); fall back to
# ImageMagick (magick, or legacy convert) where it is not -- e.g. the Windows CI runner, whose
# MSYS2 has no icoutils package. Both losslessly pack the already-rendered PNGs.
pngs=("$tmp/icon-16.png" "$tmp/icon-32.png" "$tmp/icon-48.png" "$tmp/icon-256.png")
if command -v icotool >/dev/null 2>&1; then
	icotool -c -o "$ICO" "${pngs[@]}"
elif command -v magick >/dev/null 2>&1; then
	magick "${pngs[@]}" "$ICO"
elif command -v convert >/dev/null 2>&1; then
	convert "${pngs[@]}" "$ICO"
else
	echo "gen-icons: need icotool (icoutils) or magick/convert (ImageMagick) to build $ICO" >&2
	exit 1
fi
cp "$tmp/icon-256.png" "$PNG"

# The _windows filename suffix scopes this COFF object to GOOS=windows, so the headless stub
# build (GOOS=linux) ignores it; the Go linker auto-links *.syso from the main package dir.
"$WINDRES" -I . "$RC" -O coff -o "$SYSO"

echo "gen-icons: wrote $ICO, $PNG, $SYSO"
