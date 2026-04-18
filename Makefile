VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   = -s -w -X main.Version=$(VERSION)
PKG       = ./cmd/inlinr

.PHONY: build build-all test clean install

build:
	go build -ldflags "$(LDFLAGS)" -o bin/inlinr $(PKG)

# Cross-compile release binaries. Run `make build-all VERSION=0.1.0` from a clean tag.
build-all:
	mkdir -p dist
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/inlinr-darwin-amd64  $(PKG)
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/inlinr-darwin-arm64  $(PKG)
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/inlinr-linux-amd64   $(PKG)
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/inlinr-linux-arm64   $(PKG)
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/inlinr-windows-amd64.exe $(PKG)
	cd dist && sha256sum inlinr-* > SHA256SUMS.txt

test:
	go test ./...

install:
	go install -ldflags "$(LDFLAGS)" $(PKG)

clean:
	rm -rf bin dist
