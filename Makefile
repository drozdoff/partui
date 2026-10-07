BINARY := partui

.PHONY: build test test-memory

# build creates the partui binary in the repo root
build:
	go build -o $(BINARY) ./cmd/partui

# test runs the full test suite with the race detector; the RSS measurement
# tests (build tag `memory`) are excluded — see test-memory
test:
	go test -race ./...

# test-memory runs only the peak-RSS measurement tests: they write ~1 GiB
# through the real disk per run, so they are never part of `make test`.
# Corpus size override: PARTUI_RSS_TEST_BYTES (default 256 MiB).
test-memory:
	go test -race -tags memory -run RSS ./internal/par2/
