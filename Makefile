VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.Version=$(VERSION)"

.PHONY: build install test clean

build:
	go build $(LDFLAGS) -o archmcp ./cmd/archmcp

install:
	go install $(LDFLAGS) ./cmd/archmcp

test:
	go test ./... -count=1

clean:
	rm -f archmcp
