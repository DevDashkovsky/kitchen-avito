COMPOSE ?= docker-compose

.PHONY: test test-race lint test-e2e

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	golangci-lint run ./...

test-e2e:
	@set -eu; \
	project="kitchen-avito-e2e-$$$$"; \
	cleanup() { $(COMPOSE) -p "$$project" down -v || true; }; \
	trap cleanup EXIT; \
	trap 'exit 130' INT; \
	trap 'exit 143' TERM; \
	echo "Starting isolated E2E project $$project without the establishment worker"; \
	COMPOSE_PROJECT_NAME="$$project" \
	KITCHEN_HOST_ADDRESS=127.0.0.1 \
	KITCHEN_HOST_PORT=0 \
	CONFIRMATION_TIMEOUT=5s \
	TIMEOUT_SCAN_INTERVAL=100ms \
	$(COMPOSE) -p "$$project" up -d --build postgres migrations kitchen; \
	kitchen_address="$$(COMPOSE_PROJECT_NAME="$$project" $(COMPOSE) -p "$$project" port kitchen 8080)"; \
	E2E=1 \
	KITCHEN_E2E_URL="http://$$kitchen_address" \
	E2E_CONFIRMATION_WAIT=20s \
	go test -v ./tests/e2e -count=1

.PHONY: test-postgres check-generated

test-postgres:
	bash scripts/test-postgres.sh

check-generated:
	bash scripts/check-generated.sh
