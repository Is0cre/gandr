VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS    := -s -w -X main.Version=$(VERSION) -X main.BuildDate=$(BUILD_DATE)
GOFLAGS    := -trimpath

# CGO note: gandrd is pure Go and cross-compiles freely with CGO_ENABLED=0.
# gandr links mattn/go-sqlite3 (CGO); cross-compiling it requires a target
# toolchain — native builds are the default here.

.PHONY: all build build-daemon build-client test test-short fuzz vet sign release install clean

all: test build

build: build-daemon build-client

build-daemon:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-linux-amd64      ./cmd/gandrd/
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-linux-arm64      ./cmd/gandrd/
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm   go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-linux-arm        ./cmd/gandrd/
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-darwin-arm64     ./cmd/gandrd/
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-windows-amd64.exe ./cmd/gandrd/

build-client:
	mkdir -p dist
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandr ./cmd/gandr/

test:
	go test ./... -count=1

test-short:
	go test ./... -count=1 -short

fuzz:
	bash scripts/fuzz.sh

vet:
	go vet ./...

sign:
	cd dist && sha256sum * > SHA256SUMS
	gpg --detach-sign --armor dist/SHA256SUMS

install:
	bash scripts/install.sh

release: test build sign
	gh release create $(VERSION) dist/* --title "$(VERSION)"
	bash scripts/mirror.sh

clean:
	rm -rf dist
