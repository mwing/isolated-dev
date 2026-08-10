BINARY := dev
PROXY := dev-proxy
PKG := ./...

.PHONY: all build build-proxy test lint fmt vet check clean install

all: check build

build: build-proxy
	go build -o bin/$(BINARY) ./cmd/dev

# The sidecar is built for linux/amd64 and linux/arm64 because it runs
# inside the container network, never on the host.
build-proxy:
	go build -o bin/$(PROXY) ./cmd/dev-proxy

test:
	go test -race $(PKG)

vet:
	go vet $(PKG)

# Only this module's own source. The language plugins are scaffolding
# templates — valid Go, but full of {{PROJECT_NAME}} placeholders and
# deliberately unformatted-looking commented blocks — and v1 is bash.
GOSRC := cmd internal

fmt:
	gofmt -w $(GOSRC)

lint:
	golangci-lint run

check: fmt-check vet test

fmt-check:
	@unformatted=$$(gofmt -l $(GOSRC)); \
	if [ -n "$$unformatted" ]; then echo "needs gofmt:"; echo "$$unformatted"; exit 1; fi

install: build
	install -m 0755 bin/$(BINARY) $(HOME)/.local/bin/$(BINARY)

# The sidecar image is built inside the OrbStack VM, where the agent
# containers will run. VM name follows the dev config default.
VM ?= dev-vm-docker-host
PROXY_IMAGE ?= dev-proxy:latest

# The build context excludes what the sidecar does not need. Since the
# module moved to the repository root, "everything" would now ship the v1
# tool and every language plugin to the daemon on each build.
proxy-image:
	tar -czf - --exclude=./.git --exclude=./v1 --exclude=./bin --exclude=./languages . \
	  | orb -m $(VM) sudo docker build -t $(PROXY_IMAGE) -f Dockerfile.proxy -

clean:
	rm -rf bin
