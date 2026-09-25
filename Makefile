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

check:
	go vet ./...
	cd web && npm run check
