SHELL := /bin/sh

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || printf '%s' dev)
export VERSION
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf '%s' unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
INTEGRATION_ARGS ?=
HOST_GOOS := $(shell go env GOOS)
HOST_EXE := $(if $(filter windows,$(HOST_GOOS)),.exe,)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)
TARGETS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: validate-version build run fmt fmt-check vet test check clean dist checksums integration-test

validate-version:
	@scripts/package.sh --validate-version "$$VERSION"

build: validate-version
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o "bin/javsp$(HOST_EXE)" ./cmd/javsp

run:
	go run ./cmd/javsp -config ./config.yml

fmt:
	@set -eu; \
	tmp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$tmp_dir"' EXIT HUP INT TERM; \
	find . \( -path './.git' -o -path './bin' -o -path './dist' \) -prune -o \
		-type f -name '*.go' -print >"$$tmp_dir/go-files.unsorted"; \
	LC_ALL=C sort "$$tmp_dir/go-files.unsorted" >"$$tmp_dir/go-files"; \
	while IFS= read -r file; do \
		[ ! -f "$$file" ] || gofmt -w "$$file"; \
	done <"$$tmp_dir/go-files"

fmt-check:
	@set -eu; \
	tmp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$tmp_dir"' EXIT HUP INT TERM; \
	find . \( -path './.git' -o -path './bin' -o -path './dist' \) -prune -o \
		-type f -name '*.go' -print >"$$tmp_dir/go-files.unsorted"; \
	LC_ALL=C sort "$$tmp_dir/go-files.unsorted" >"$$tmp_dir/go-files"; \
	: >"$$tmp_dir/unformatted"; \
	while IFS= read -r file; do \
		[ ! -f "$$file" ] || gofmt -l "$$file" >>"$$tmp_dir/unformatted"; \
	done <"$$tmp_dir/go-files"; \
	if [ -s "$$tmp_dir/unformatted" ]; then \
		cat "$$tmp_dir/unformatted"; \
		exit 1; \
	fi

vet:
	go vet ./...

test:
	go test ./...

check: fmt-check vet test build

clean:
	rm -rf bin dist javsp javsp.exe

dist: validate-version
	rm -rf dist
	mkdir -p dist/bin
	@set -eu; \
	for target in $(TARGETS); do \
		goos=$${target%/*}; \
		goarch=$${target#*/}; \
		executable=javsp; \
		[ "$$goos" != windows ] || executable=javsp.exe; \
		binary="dist/bin/$${goos}_$${goarch}/$$executable"; \
		mkdir -p "dist/bin/$${goos}_$${goarch}"; \
		printf 'building %s/%s\n' "$$goos" "$$goarch"; \
		CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" go build -trimpath \
			-ldflags "$(LDFLAGS)" -o "$$binary" ./cmd/javsp; \
		scripts/package.sh "$$VERSION" "$$goos" "$$goarch" "$$binary" dist; \
	done
	rm -rf dist/bin

checksums:
	scripts/verify-release.sh dist
	@set -eu; cd dist; \
	if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum javsp-go_*.tar.gz javsp-go_*.zip >SHA256SUMS; \
	else \
		shasum -a 256 javsp-go_*.tar.gz javsp-go_*.zip >SHA256SUMS; \
	fi

integration-test:
	@if [ ! -x scripts/integration-test.sh ]; then \
		printf '%s\n' 'scripts/integration-test.sh is not available' >&2; \
		exit 1; \
	fi
	scripts/integration-test.sh $(INTEGRATION_ARGS)
