SHELL := /bin/sh

TB_VERSION := 0.17.9
TB_DATA_FILE := $(CURDIR)/.data/tigerbeetle/0_0.tigerbeetle
BINARY := $(CURDIR)/bin/ledger-service
LOADTEST_BINARY := $(CURDIR)/bin/loadtest

.PHONY: build test test-race vet fmt-check check tb-format up down logs

build:
	@mkdir -p "$(dir $(BINARY))"
	@if [ "$$(uname -s)" = "Darwin" ] && [ "$$(uname -m)" = "arm64" ]; then \
		CGO_LDFLAGS="-Wl,-ld_classic" go build -o "$(BINARY)" ./cmd/ledger-service && \
		CGO_LDFLAGS="-Wl,-ld_classic" go build -o "$(LOADTEST_BINARY)" ./cmd/loadtest; \
	else \
		go build -o "$(BINARY)" ./cmd/ledger-service && \
		go build -o "$(LOADTEST_BINARY)" ./cmd/loadtest; \
	fi

# macOS 26's new linker rejects the upstream 0.17.9 arm64 archive's alignment.
# The classic-linker flag is scoped to local tests; Linux/Docker needs no workaround.
test:
	@if [ "$$(uname -s)" = "Darwin" ] && [ "$$(uname -m)" = "arm64" ]; then \
		CGO_LDFLAGS="-Wl,-ld_classic" go test ./...; \
	else \
		go test ./...; \
	fi

test-race:
	@if [ "$$(uname -s)" = "Darwin" ] && [ "$$(uname -m)" = "arm64" ]; then \
		CGO_LDFLAGS="-Wl,-ld_classic" go test -race ./...; \
	else \
		go test -race ./...; \
	fi

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './.data/*'))"

check: fmt-check test test-race vet build

tb-format:
	@mkdir -p "$(dir $(TB_DATA_FILE))"
	@if [ ! -f "$(TB_DATA_FILE)" ]; then \
		docker run --rm --security-opt seccomp=unconfined --ulimit memlock=-1:-1 \
			-v "$(dir $(TB_DATA_FILE)):/data" \
			ghcr.io/tigerbeetle/tigerbeetle:$(TB_VERSION) \
			format --cluster=0 --replica=0 --replica-count=1 /data/0_0.tigerbeetle; \
	fi

up: tb-format
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f service tigerbeetle
