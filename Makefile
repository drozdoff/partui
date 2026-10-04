BINARY := partui

.PHONY: build test

# build creates the partui binary in the repo root
build:
	go build -o $(BINARY) ./cmd/partui

# test runs the full test suite with the race detector
test:
	go test -race ./...
