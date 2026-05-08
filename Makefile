VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build install test lint clean tidy release-dryrun

build:
	go build -ldflags "$(LDFLAGS)" -o bin/chx ./cmd/chx

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/chx

test:
	go test -race -cover ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

release-dryrun:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin/ dist/
