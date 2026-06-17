.PHONY: test build fmt

fmt:
	go fmt ./...

test:
	go test ./...

build:
	go build -o quack-cli ./cmd/quack
	go build -o quack-server ./cmd/quack-server


export:
	repomix cmd internal

install-cli:
	go build -o quack-cli ./cmd/quack
	sudo mv quack-cli /usr/local/bin/
