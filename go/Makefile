BINARY := dev2
PROXY := dev2-proxy
PKG := ./...

.PHONY: all build build-proxy test lint fmt vet check clean install

all: check build

build: build-proxy
	go build -o bin/$(BINARY) ./cmd/dev2

# The sidecar is built for linux/amd64 and linux/arm64 because it runs
# inside the container network, never on the host.
build-proxy:
	go build -o bin/$(PROXY) ./cmd/dev2-proxy

test:
	go test -race $(PKG)

vet:
	go vet $(PKG)

fmt:
	gofmt -w .

lint:
	golangci-lint run

check: fmt-check vet test

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "needs gofmt:"; echo "$$unformatted"; exit 1; fi

install: build
	install -m 0755 bin/$(BINARY) $(HOME)/.local/bin/$(BINARY)

clean:
	rm -rf bin
