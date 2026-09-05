.PHONY: all build test vet race fuzz run fmt docker-build docker-run clean

BINARY ?= go-serve
IMAGE ?= go-serve:dev

all: test build

build:
	go build -trimpath -ldflags='-s -w' -o bin/$(BINARY) ./cmd/server

test:
	go test ./...

vet:
	go vet ./...

race:
	CGO_ENABLED=1 go test -race ./...

fuzz:
	go test ./internal/config -fuzz=FuzzParseTOML -fuzztime=10s

run:
	go run ./cmd/server -config config/example.toml

fmt:
	gofmt -w cmd internal

docker-build:
	docker build --tag $(IMAGE) .

docker-run:
	docker run --rm --publish 8080:8080 --publish 9901:9901 $(IMAGE)

clean:
	rm -rf bin
