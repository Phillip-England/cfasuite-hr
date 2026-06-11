PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

.PHONY: build install test run

build:
	mkdir -p bin
	go build -o bin/cfasuite-hr .

install:
	GOBIN=$(BINDIR) go install .

test:
	go test ./...

run:
	go run . serve
