BINARY  := latprobe
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
IMAGE   ?= latprobe:$(VERSION)

.PHONY: all build test race cover vet fmt lint run serve docker compose-up compose-down clean

all: fmt vet test build

build: ## Build a static binary into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/latprobe

test: ## Run unit tests
	go test ./...

race: ## Run tests with the race detector
	go test -race ./...

cover: ## Test coverage summary
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

vet:
	go vet ./...

fmt:
	gofmt -s -w .

lint: ## Requires golangci-lint
	golangci-lint run

run: build ## One-shot run against the example config
	./bin/$(BINARY) run -c examples/config.yaml -n 5 -i 1s

serve: build ## Serve metrics on :9310
	./bin/$(BINARY) serve -c examples/config.yaml

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

compose-up: ## latprobe + Prometheus + Grafana
	docker compose up -d --build

compose-down:
	docker compose down

clean:
	rm -rf bin dist coverage.out
