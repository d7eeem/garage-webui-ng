
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	CGO_ENABLED=0 go build -o main -tags="prod" -ldflags="-X main.version=$(VERSION)" .

run:
	go run .
