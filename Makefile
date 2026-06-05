VERSION ?= 0.1.0+$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build deb client test lint vet fmt tidy clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ./lanweaved ./cmd/lanweaved

# Build the server Debian package into dist/. Requires nfpm (github.com/goreleaser/nfpm).
deb: build
	mkdir -p dist
	VERSION="$(VERSION)" nfpm pkg --packager deb --config packaging/nfpm.yaml --target dist/

# The Windows client GUI is built on Windows (or a Windows cross-toolchain):
#   go build -tags gui -ldflags "$(LDFLAGS)" -o lanweave-client.exe ./cmd/lanweave-client
# then packaged with NSIS (packaging/windows/lanweave-client.nsi) alongside wintun.dll.
client:
	@echo "Build the Windows client with -tags gui on Windows; see packaging/windows/ and INSTALL.md."

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

tidy:
	go mod tidy

clean:
	rm -rf ./run ./lanweaved ./dist
