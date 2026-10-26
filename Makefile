SHELL := /bin/bash
.DEFAULT_GOAL := help

.PHONY: help mongo backend frontend import up down logs build test integration-test fmt clean

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

mongo: ## Start MongoDB only (for local backend development)
	docker compose up -d mongo
	@echo "MongoDB is available at mongodb://localhost:27017"

backend: ## Run the Go API on :8080 (reads ../.env when present)
	cd backend && go run ./cmd/server

frontend: ## Run the Astro dev server on :4321 (loads ../.env)
	cd frontend && npm run dev

import: ## Bulk-import repositories (ARGS="https://github.com/org")
	./import $(ARGS)

up: ## Build and start mongo + backend + frontend with Docker
	docker compose up --build -d
	@echo "Dashboard: http://localhost:$${FRONTEND_PORT:-4321}"

down: ## Stop the Docker stack
	docker compose down

logs: ## Follow Docker logs
	docker compose logs -f

build: ## Build backend and frontend locally
	cd backend && go build ./...
	cd frontend && npm run build

test: ## Run backend tests and frontend type checks
	cd backend && go test ./...
	cd frontend && npm run check

integration-test: ## Run MongoDB-backed store tests (MONGODB_TEST_URI required)
	cd backend && go test -tags=integration ./internal/store/...

fmt: ## Format Go code
	cd backend && gofmt -w .

clean: ## Remove local build artifacts
	rm -rf frontend/dist frontend/.astro
	cd backend && go clean
