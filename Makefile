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
	docker-build docker-tag docker-push k8s-release \
	docker-image test tidy fmt lint

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

docker-build: build-linux-arm64
	@echo "==> building ARM64 Docker image"
	@docker build --platform linux/arm64 -t $(DOCKER_IMAGE):latest .

docker-tag:
	@tag="$(TAG)"; \
	if [ -z "$$tag" ]; then \
		read -p "Enter image tag: " tag; \
	fi; \
	if [ -z "$$tag" ]; then \
		echo "Error: TAG is required"; \
		exit 1; \
	fi; \
	echo "==> tagging $(DOCKER_IMAGE):latest as $(DOCKER_IMAGE):$$tag"; \
	docker tag $(DOCKER_IMAGE):latest $(DOCKER_IMAGE):$$tag

docker-push:
	@tag="$(TAG)"; \
	if [ -z "$$tag" ]; then \
		read -p "Enter image tag to push (default 'latest'): " tag; \
	fi; \
	if [ -z "$$tag" ]; then \
		tag="latest"; \
	fi; \
	echo "==> pushing $(DOCKER_IMAGE):$$tag"; \
	docker push $(DOCKER_IMAGE):$$tag

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

test:
	@go test ./...

tidy:
	@go mod tidy

fmt:
	@gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

lint:
	@$(GOLANGCI_LINT) run ./cmd/... ./container/... ./config/... ./infrastructure/metrics/...
