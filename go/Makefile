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

# The sidecar image is built inside the OrbStack VM, where the agent
# containers will run. VM name follows the dev config default.
VM ?= dev-vm-docker-host
PROXY_IMAGE ?= dev2-proxy:latest

proxy-image:
	tar -czf - . | orb -m $(VM) sudo docker build -t $(PROXY_IMAGE) -f Dockerfile.proxy -

clean:
	rm -rf bin
