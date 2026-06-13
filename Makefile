.PHONY: test build fmt

fmt:
	go fmt ./...

test:
	go test ./...

build:
	go build -o quack ./cmd/quack
	go build -o quack-server ./cmd/quack-server
