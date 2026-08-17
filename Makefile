.PHONY: build test vet lint-boundary lint-boundary-selftest lint-boundary-replace-selftest lint-docs check

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

lint-boundary:
	./scripts/lint-boundary.sh

# Prove Rule A can actually fail. Points it at a dependency sdk/ genuinely has
# (gosnmp); if that does NOT trip, the lint is inert and its green result on
# openits-models means nothing.
#
# A nonzero exit alone is NOT sufficient evidence and is deliberately not what
# this asserts. The script exits 2 when `go list` fails and 1 on a genuine
# openits-models violation, so "it failed" would read as "the rule fires" in
# three unrelated situations, two of which mean the selftest proved nothing.
# The grep pins the exact Rule A message, so only Rule A firing on the injected
# dependency counts as a pass.
lint-boundary-selftest:
	@out=$$(LINT_FORBIDDEN=gosnmp ./scripts/lint-boundary.sh 2>&1); \
	status=$$?; \
	if [ $$status -eq 0 ]; then \
		echo "SELFTEST FAILED: lint-boundary did not flag a dependency that exists" >&2; \
		printf '%s\n' "$$out" >&2; \
		exit 1; \
	fi; \
	if ! printf '%s\n' "$$out" | grep -q 'BOUNDARY VIOLATION (transitive).*gosnmp'; then \
		echo "SELFTEST FAILED: lint-boundary failed, but not with Rule A's transitive violation on gosnmp -- the failure proves nothing about the rule" >&2; \
		printf '%s\n' "$$out" >&2; \
		exit 1; \
	fi; \
	echo "lint-boundary selftest: Rule A fires correctly"

# Prove Rule C can actually fail. Points the rule at a fixture go.mod that DOES
# replace the model module; if that does not trip, the rule is inert and its
# green result on the real go.mod means nothing.
#
# Same reasoning as the target above for grepping rather than trusting the exit
# status: an unreadable LINT_GOMOD makes awk exit 2, which under a bare
# exit-status check is indistinguishable from Rule C firing. The grep pins Rule
# C's own message.
lint-boundary-replace-selftest:
	@tmp=$$(mktemp -d); \
	printf 'module example.com/x\n\ngo 1.26\n\nreplace github.com/Vikasa2M/openits-models => ../openits-models\n' > $$tmp/go.mod; \
	out=$$(LINT_GOMOD=$$tmp/go.mod ./scripts/lint-boundary.sh 2>&1); \
	status=$$?; \
	rm -rf $$tmp; \
	if [ $$status -eq 0 ]; then \
		echo "SELFTEST FAILED: lint-boundary did not flag a replace directive" >&2; \
		printf '%s\n' "$$out" >&2; \
		exit 1; \
	fi; \
	if ! printf '%s\n' "$$out" | grep -q 'BOUNDARY VIOLATION (replace directive).*openits-models'; then \
		echo "SELFTEST FAILED: lint-boundary failed, but not with Rule C's replace-directive violation -- the failure proves nothing about the rule" >&2; \
		printf '%s\n' "$$out" >&2; \
		exit 1; \
	fi; \
	echo "lint-boundary replace-rule selftest: Rule C fires correctly"

lint-docs:
	./scripts/lint-docs.sh

check: vet test lint-boundary lint-boundary-selftest lint-boundary-replace-selftest lint-docs
