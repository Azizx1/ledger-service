SHELL := /bin/sh

TB_VERSION := 0.17.9
TB_DATA_FILE := $(CURDIR)/.data/tigerbeetle/0_0.tigerbeetle
TB_NATIVE_DIR := $(CURDIR)/.tools/tigerbeetle
TB_NATIVE_BINARY := $(TB_NATIVE_DIR)/tigerbeetle
TB_NATIVE_ARCHIVE := $(CURDIR)/.tools/tigerbeetle-$(TB_VERSION).zip
BINARY := $(CURDIR)/bin/ledger-service
LOADTEST_BINARY := $(CURDIR)/bin/loadtest

.PHONY: build test test-race vet fmt-check check dev-tb dev-api dev-reset tb-native-format tb-format up down logs

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

$(TB_NATIVE_BINARY):
	@set -eu; \
	command -v curl >/dev/null 2>&1 || { echo "curl is required to download TigerBeetle" >&2; exit 1; }; \
	command -v unzip >/dev/null 2>&1 || { echo "unzip is required to install TigerBeetle" >&2; exit 1; }; \
	case "$$(uname -s)/$$(uname -m)" in \
		Darwin/*) asset="tigerbeetle-universal-macos.zip" ;; \
		Linux/x86_64|Linux/amd64) asset="tigerbeetle-x86_64-linux.zip" ;; \
		Linux/arm64|Linux/aarch64) asset="tigerbeetle-aarch64-linux.zip" ;; \
		*) echo "unsupported platform: $$(uname -s)/$$(uname -m)" >&2; exit 1 ;; \
	esac; \
	mkdir -p "$(TB_NATIVE_DIR)"; \
	echo "Downloading TigerBeetle $(TB_VERSION) ($$asset)..."; \
	curl -fL --retry 2 \
		"https://github.com/tigerbeetle/tigerbeetle/releases/download/$(TB_VERSION)/$$asset" \
		-o "$(TB_NATIVE_ARCHIVE).tmp"; \
	mv "$(TB_NATIVE_ARCHIVE).tmp" "$(TB_NATIVE_ARCHIVE)"; \
	unzip -qo "$(TB_NATIVE_ARCHIVE)" -d "$(TB_NATIVE_DIR)"; \
	chmod +x "$(TB_NATIVE_BINARY)"

tb-native-format: $(TB_NATIVE_BINARY)
	@mkdir -p "$(dir $(TB_DATA_FILE))"
	@if [ ! -f "$(TB_DATA_FILE)" ]; then \
		"$(TB_NATIVE_BINARY)" format \
			--cluster=0 --replica=0 --replica-count=1 "$(TB_DATA_FILE)"; \
	fi

dev-tb: tb-native-format
	@echo "TigerBeetle listening on 127.0.0.1:3000"
	@exec "$(TB_NATIVE_BINARY)" start --addresses=3000 "$(TB_DATA_FILE)"

dev-api: build
	@echo "Ledger API listening on http://127.0.0.1:8080"
	@HTTP_ADDRESS="$${HTTP_ADDRESS:-127.0.0.1:8080}" \
		TB_ADDRESSES="$${TB_ADDRESSES:-127.0.0.1:3000}" \
		exec "$(BINARY)"

dev-reset:
	@if command -v lsof >/dev/null 2>&1; then \
		for port in 3000 8080; do \
			if lsof -nP -iTCP:$$port -sTCP:LISTEN >/dev/null 2>&1; then \
				echo "port $$port is still in use; stop dev-tb and dev-api before resetting" >&2; \
				exit 1; \
			fi; \
		done; \
	fi
	@echo "Deleting all local TigerBeetle ledger data..."
	@rm -rf -- "$(CURDIR)/.data/tigerbeetle"

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
