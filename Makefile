# Traceboard build and verification targets.
#
# `go-build` depends on `web-build` so a release binary always embeds dashboard
# assets built from the current web workspace. `tests/make-order.sh` asserts
# that dependency, so a stale binary cannot pass verification.

build: go-build

web-build:
	cd web && npm ci && npm run build

go-build: web-build
	@mkdir -p bin
	go build -o bin/traceboard ./cmd/traceboard

test:
	sh tests/make-order.sh
	go test ./...
	cd web && npm run test:run

race:
	go test -race ./...

check:
	go vet ./...
	cd web && npm run check

integration:
	go test -tags=integration ./tests/integration/...

# The browser binary only; installing operating-system packages needs root and
# belongs to CI, which runs this step on a runner that can do it.
e2e: go-build
	cd web && npx playwright install chromium
	cd web && npm run test:e2e

# Every check that gates a release, in the order a reviewer would run them.
verify: check test race integration e2e
	./bin/traceboard version

clean:
	rm -rf bin web/test-results web/playwright-report

.PHONY: build web-build go-build test race check integration e2e verify clean
