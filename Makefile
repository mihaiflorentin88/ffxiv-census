APP_NAME := ffxiv-census
DIST_DIR := dist
BUILD_DIR := bin
MAIN_PKG := .
DOCKER_IMAGE ?= mihaiflorentin88/census
GOLANGCI_LINT ?= $(shell which golangci-lint 2>/dev/null || echo $(HOME)/go/bin/golangci-lint)

.PHONY: build clean build-all \
	build-linux-amd64 build-linux-arm64 \
	build-darwin-amd64 build-darwin-arm64 \
	build-windows-amd64 build-windows-arm64 \
	docker-build docker-tag k8s-release \
	docker-image test tidy fmt lint postgres postgres-stop

build:
	@echo "==> building $(APP_NAME)"
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(APP_NAME) $(MAIN_PKG)

clean:
	@echo "==> cleaning build artifacts"
	@rm -rf $(BUILD_DIR) $(DIST_DIR)

build-all: clean \
	build-linux-amd64 build-linux-arm64 \
	build-darwin-amd64 build-darwin-arm64 \
	build-windows-amd64 build-windows-arm64

build-linux-amd64:
	@echo "==> linux/amd64"
	@mkdir -p $(DIST_DIR)
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST_DIR)/$(APP_NAME)-linux-amd64 $(MAIN_PKG)

build-linux-arm64:
	@echo "==> linux/arm64"
	@mkdir -p $(DIST_DIR)
	@GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o $(DIST_DIR)/$(APP_NAME)-linux-arm64 $(MAIN_PKG)

build-darwin-amd64:
	@echo "==> darwin/amd64"
	@mkdir -p $(DIST_DIR)
	@GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST_DIR)/$(APP_NAME)-darwin-amd64 $(MAIN_PKG)

build-darwin-arm64:
	@echo "==> darwin/arm64"
	@mkdir -p $(DIST_DIR)
	@GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o $(DIST_DIR)/$(APP_NAME)-darwin-arm64 $(MAIN_PKG)

build-windows-amd64:
	@echo "==> windows/amd64"
	@mkdir -p $(DIST_DIR)
	@GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST_DIR)/$(APP_NAME)-windows-amd64.exe $(MAIN_PKG)

build-windows-arm64:
	@echo "==> windows/arm64"
	@mkdir -p $(DIST_DIR)
	@GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -o $(DIST_DIR)/$(APP_NAME)-windows-arm64.exe $(MAIN_PKG)

docker-build: build-linux-arm64 build-linux-amd64
	@echo "==> building and pushing multi-arch Docker image $(DOCKER_IMAGE):latest (linux/amd64 + linux/arm64)"
	@docker buildx inspect multiarch >/dev/null 2>&1 || docker buildx create --name multiarch --driver docker-container >/dev/null
	@docker buildx build --builder multiarch --platform linux/amd64,linux/arm64 \
		--provenance=false \
		-t $(DOCKER_IMAGE):latest --push .

docker-tag:
	@tag="$(TAG)"; \
	if [ -z "$$tag" ]; then \
		read -p "Enter image tag: " tag; \
	fi; \
	if [ -z "$$tag" ]; then \
		echo "Error: TAG is required"; \
		exit 1; \
	fi; \
	echo "==> tagging $(DOCKER_IMAGE):latest as $(DOCKER_IMAGE):$$tag (multi-arch manifest)"; \
	docker buildx imagetools create -t $(DOCKER_IMAGE):$$tag $(DOCKER_IMAGE):latest

k8s-release:
	@tag="$(TAG)"; \
	if [ -z "$$tag" ]; then \
		read -p "Enter release TAG: " tag; \
	fi; \
	if [ -z "$$tag" ]; then \
		echo "Error: TAG is required"; \
		exit 1; \
	fi; \
	echo "==> releasing to Kubernetes with TAG=$$tag"; \
	$(MAKE) -C k8s deploy TAG=$$tag
docker-image:
	@echo "==> building runtime Docker image"
	@docker build -t $(APP_NAME):latest .

postgres:
	@echo "==> starting local PostgreSQL container"
	@docker run --rm -d --name ffxiv-postgres -e POSTGRES_USER=census -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=ffxiv_census -p 5432:5432 postgres:16-alpine

postgres-stop:
	@echo "==> stopping local PostgreSQL container"
	@docker stop ffxiv-postgres || true

test:
	@go test -p 1 ./...
tidy:
	@go mod tidy

fmt:
	@gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

lint:
	@$(GOLANGCI_LINT) run ./cmd/... ./container/... ./config/... ./infrastructure/metrics/...
