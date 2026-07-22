.PHONY: build test vet lint-boundary lint-boundary-selftest check

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

lint-boundary:
	./scripts/lint-boundary.sh

# Prove the boundary rule can actually fail. Points it at a dependency sdk/
# genuinely has (gosnmp); if that does NOT trip, the lint is inert and its
# green result on openits-models means nothing.
lint-boundary-selftest:
	@if LINT_FORBIDDEN=gosnmp ./scripts/lint-boundary.sh 2>/dev/null; then \
		echo "SELFTEST FAILED: lint-boundary did not flag a dependency that exists" >&2; \
		exit 1; \
	else \
		echo "lint-boundary selftest: rule fires correctly"; \
	fi

check: vet test lint-boundary lint-boundary-selftest
