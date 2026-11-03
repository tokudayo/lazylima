VERSION ?= $(shell cat VERSION)

.PHONY: run demo build test check release
run:
	go run ./cmd/lazylima

demo:
	go run ./cmd/lazylima --demo

build:
	go build -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/lazylima ./cmd/lazylima

test:
	go test ./...

check:
	go test -race ./...
	go vet ./...

release:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o dist/lazylima-darwin-arm64 ./cmd/lazylima
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o dist/lazylima-darwin-amd64 ./cmd/lazylima
