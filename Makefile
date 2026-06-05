VERSION ?= 0.1.0+$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test lint vet fmt tidy clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ./lanweaved ./cmd/lanweaved

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
	rm -rf ./run ./lanweaved
