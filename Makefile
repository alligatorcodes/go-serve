.PHONY: all build test vet race fuzz run run-single run-cluster fmt docker-build docker-run clean

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
	$(MAKE) run-single

run-single:
	go run ./cmd/server -config config/example.toml

run-cluster:
	@set -eu; \
	trap 'status=$$?; trap - EXIT INT TERM; kill 0 2>/dev/null || true; exit $$status' EXIT INT TERM; \
	go run ./cmd/server -config config/example-cluster-node-1.toml & \
	go run ./cmd/server -config config/example-cluster-node-2.toml & \
	go run ./cmd/server -config config/example-cluster-node-3.toml & \
	wait

fmt:
	gofmt -w cmd internal

docker-build:
	docker build --tag $(IMAGE) .

docker-run:
	docker run --rm --publish 8080:8080 --publish 9901:9901 $(IMAGE)

clean:
	rm -rf bin
