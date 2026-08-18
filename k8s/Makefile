HELM ?= $(shell which helm 2>/dev/null || echo /usr/local/bin/helm)
RELEASE_NAME ?= ffxiv-census

.PHONY: help
help: ## Display this help message
	@echo "Usage: make [target]"
	@echo
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*##"; printf "\033[36m%-30s\033[0m %s\n", "Target", "Description"} /^[a-zA-Z_-]+:.*?##/ { printf "\033[36m%-30s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: deploy
deploy: ## Deploy the Helm chart to Kubernetes (requires TAG=<version>)
	@if [ -z "$(TAG)" ]; then \
		echo "Error: TAG variable should not be empty"; \
		exit 1; \
	fi
	@if [ "$(TAG)" = "updateMe" ]; then \
		echo "Error: TAG variable cannot be 'updateMe'. Please provide a valid image tag."; \
		exit 1; \
	fi
	$(HELM) -n default upgrade -i $(RELEASE_NAME) . \
		--set image.tag=${TAG} \
		--wait \
		--timeout 3m0s \
		-f values.yaml

.PHONY: post-deploy-check
post-deploy-check: ## Check deployment status
	@echo "Checking if the deployment was successful"
	@sleep 1
	kubectl -n default rollout status deployment
