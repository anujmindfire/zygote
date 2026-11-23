.PHONY: all build test lint clean check

all: check build

build:
	@echo "==> Building zyg CLI..."
	@go build -o bin/zyg cmd/zyg/main.go

test:
	@echo "==> Running tests..."
	@go test -v -race -cover ./...
	@echo "==> Running adapter tests..."
	@(cd source/agentos && go test -v -race .)
	@(cd source/e2b && go test -v -race .)
	@(cd source/daytona && go test -v -race .)

lint:
	@echo "==> Running golangci-lint..."
	@golangci-lint run 2>/dev/null || golangci-lint run --no-config -E govet,errcheck,staticcheck,ineffassign,gosec,gocritic,revive,bodyclose,noctx

clean:
	@echo "==> Cleaning..."
	@rm -rf bin/
	@go clean

conformance:
	@echo "==> Running format and conformance test suite..."
	@go test -v -race ./spec/conformance/...

check: lint test conformance
	@echo "==> Running TTFV walkthrough..."
	@bash scripts/ttfv_test.sh
	@echo "==> All checks passed!"
