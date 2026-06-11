VERSION ?= 0.1.0+$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build deb client icons routerd routerd-cross test lint vet fmt tidy clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ./lanweaved ./cmd/lanweaved

# Build the server Debian package into dist/. Requires nfpm (github.com/goreleaser/nfpm).
deb: build
	mkdir -p dist
	VERSION="$(VERSION)" nfpm pkg --packager deb --config packaging/nfpm.yaml --target dist/

# Regenerate the app icon assets from packaging/icon.svg: the multi-size icon.ico (EXE + NSIS),
# internal/client/ui/icon.png (Fyne window icon), and the gitignored resources_windows.syso the
# Go linker embeds into the EXE. Needs rsvg-convert, icotool, and a MinGW windres.
icons:
	./packaging/scripts/gen-icons.sh

# The Windows client GUI is built on Windows (or a Windows cross-toolchain):
#   go build -tags gui -ldflags "$(LDFLAGS)" -o lanweave-client.exe ./cmd/lanweave-client
# then packaged with NSIS (packaging/windows/lanweave-client.nsi) alongside wintun.dll.
# Depends on `icons` so the EXE resource object is present before building.
client: icons
	@echo "Build the Windows client with -tags gui on Windows; see packaging/windows/ and docs/GUIDE.en.md."

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

# Runs gofmt check, go vet, and staticcheck if available.
lint: vet
	@test -z "$$(gofmt -l .)" || (echo "gofmt: files need formatting:"; gofmt -l .; exit 1)
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed; skipping"

# OpenWrt router client (feature 031): single static binary, kernel WireGuard.
routerd:
	CGO_ENABLED=0 go build -o dist/lanweave-routerd ./cmd/lanweave-routerd

# Cross targets for the supported router architectures (modern devices,
# >=64MB flash). mipsle needs softfloat on common MT76xx SoCs.
routerd-cross:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/lanweave-routerd-amd64 ./cmd/lanweave-routerd
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/lanweave-routerd-arm64 ./cmd/lanweave-routerd
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -o dist/lanweave-routerd-mipsle ./cmd/lanweave-routerd

tidy:
	go mod tidy

clean:
	rm -rf ./run ./lanweaved ./dist
