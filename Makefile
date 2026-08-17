.PHONY: build test vet lint-boundary lint-boundary-selftest lint-boundary-replace-selftest lint-docs check

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

# Prove Rule C can actually fail. Points the rule at a fixture go.mod that DOES
# replace the model module; if that does not trip, the rule is inert and its
# green result on the real go.mod means nothing.
lint-boundary-replace-selftest:
	@tmp=$$(mktemp -d); \
	printf 'module example.com/x\n\ngo 1.26\n\nreplace github.com/Vikasa2M/openits-models => ../openits-models\n' > $$tmp/go.mod; \
	if LINT_GOMOD=$$tmp/go.mod ./scripts/lint-boundary.sh >/dev/null 2>&1; then \
		rm -rf $$tmp; \
		echo "SELFTEST FAILED: lint-boundary did not flag a replace directive" >&2; \
		exit 1; \
	else \
		rm -rf $$tmp; \
		echo "lint-boundary replace-rule selftest: rule fires correctly"; \
	fi

lint-docs:
	./scripts/lint-docs.sh

check: vet test lint-boundary lint-boundary-selftest lint-boundary-replace-selftest lint-docs
