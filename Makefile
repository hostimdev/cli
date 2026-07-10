SPEC_URL ?= https://api.hostim.dev/openapi.public.json
BIN      ?= hostim
PREFIX   ?= /usr/local
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X github.com/hostimdev/cli/internal/cmd.version=$(VERSION)

.PHONY: generate build install test lint fmt tidy clean

# Regenerate the API client directly from the LIVE hosted spec — no vendored
# spec file. Only api/client.gen.go is committed. CI re-runs this and fails on a
# diff (see .github/workflows/generate.yml).
generate:
	@command -v oapi-codegen >/dev/null 2>&1 || go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	@tmp=$$(mktemp) && \
		echo "fetching live spec: $(SPEC_URL)" && \
		curl -fsSL --max-time 30 "$(SPEC_URL)" -o $$tmp && \
		oapi-codegen -config api/oapi.cfg.yaml $$tmp && \
		rm -f $$tmp && \
		echo "wrote api/client.gen.go"

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) .

install: build
	install -Dm755 bin/$(BIN) $(PREFIX)/bin/$(BIN)

test:
	go test ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf bin
