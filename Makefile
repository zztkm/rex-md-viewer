BINARY := bin/rex-md-viewer
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test check install clean smoke smoke-rex smoke-links-rex
build:
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

test:
	go test -race ./...

check:
	go vet ./...
	test -z "$$(gofmt -l *.go)"

smoke: build
	python3 scripts/smoke_terminal.py ./$(BINARY)

smoke-rex: build
	python3 scripts/smoke_rex.py ./$(BINARY)

smoke-links-rex:
	@tmp=$$(mktemp -d) || exit 1; trap 'rm -rf "$$tmp"' EXIT; \
	go test -c -o "$$tmp/link-tests" . && \
	python3 scripts/smoke_links_rex.py "$$tmp/link-tests"

install:
	go install -trimpath -ldflags "-X main.version=$(VERSION)" .

clean:
	rm -rf bin
