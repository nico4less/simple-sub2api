BINARY ?= simple-sub2api
BUILD_DIR ?= build
VERSION ?= dev
COMMIT ?= unknown
DATE ?= unknown
BUILDVCS ?= false
LDFLAGS := -s -w -X github.com/0xForce-Network/simple-sub2api/internal/version.Version=$(VERSION) -X github.com/0xForce-Network/simple-sub2api/internal/version.Commit=$(COMMIT) -X github.com/0xForce-Network/simple-sub2api/internal/version.Date=$(DATE)
GO_BUILD := go build -buildvcs=$(BUILDVCS) -trimpath -ldflags "$(LDFLAGS)"
NODE_IMAGE ?= node:20-bookworm-slim

.PHONY: test frontend-install frontend-build docker-build build linux-amd64 darwin-amd64 darwin-arm64 windows-amd64 docker-image clean

test:
	go test ./...

frontend-install:
	docker run --rm -v $(CURDIR)/frontend:/workspace -w /workspace $(NODE_IMAGE) npm install

frontend-build:
	docker run --rm -v $(CURDIR):/workspace -w /workspace/frontend $(NODE_IMAGE) npm run build

docker-build:
	docker build -t simple-sub2api:e001-clean-build .

build:
	$(GO_BUILD) -o $(BUILD_DIR)/$(BINARY) ./cmd/simple-sub2api

linux-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO_BUILD) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/simple-sub2api

darwin-amd64:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO_BUILD) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 ./cmd/simple-sub2api

darwin-arm64:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO_BUILD) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/simple-sub2api

windows-amd64:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO_BUILD) -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe ./cmd/simple-sub2api

docker-image:
	docker build -t simple-sub2api:local .

clean:
	rm -rf $(BUILD_DIR)
