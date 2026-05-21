BINARY ?= simple-sub2api
VERSION ?= dev
COMMIT ?= unknown
DATE ?= unknown
LDFLAGS := -s -w -X github.com/0xForce-Network/simple-sub2api/internal/version.Version=$(VERSION) -X github.com/0xForce-Network/simple-sub2api/internal/version.Commit=$(COMMIT) -X github.com/0xForce-Network/simple-sub2api/internal/version.Date=$(DATE)

.PHONY: test build linux-amd64 darwin-amd64 darwin-arm64 windows-amd64 docker-image clean

test:
	go test ./...

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY) ./cmd/simple-sub2api

linux-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/simple-sub2api

darwin-amd64:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 ./cmd/simple-sub2api

darwin-arm64:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 ./cmd/simple-sub2api

windows-amd64:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe ./cmd/simple-sub2api

docker-image:
	docker build -t simple-sub2api:local .

clean:
	rm -rf dist
