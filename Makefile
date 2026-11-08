# Smart Campaign Composer — developer entry points (KICKOFF §11).
# Money guard (need.md N-PLAN-014): up/test/e2e/stress force LLM_PROVIDER=mock; only `smoke-live` and
# `eval PROVIDER=anthropic` can reach the paid API, and both refuse to run without CONFIRM_SPEND=yes.

SHELL        := /bin/bash
COMPOSE      := docker compose
TEST_COMPOSE := docker compose -p scc-test --profile test
HOST_TEST_DB ?= postgres://scc:scc@localhost:55432/postgres?sslmode=disable

.DEFAULT_GOAL := help
.PHONY: help up down logs ps test test-host lint fmt schema dev-sub stress e2e eval smoke-live \
        sanitize-check fresh-clone-check harvest-sessions

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-18s %s\n", $$1, $$2}'

up: ## Build and start the stack with the mock LLM (never spends money)
	LLM_PROVIDER=mock $(COMPOSE) up -d --build --wait

down: ## Stop the stack (keeps the database volume)
	$(COMPOSE) down

logs: ## Follow logs of all services
	$(COMPOSE) logs -f --tail=200

ps: ## Show service status
	$(COMPOSE) ps

test: ## go vet + race tests + web unit tests in containers; no key, no internet (R-AI-2, R-DEL-3)
	@set -e; trap '$(TEST_COMPOSE) down -v --remove-orphans >/dev/null 2>&1' EXIT; \
	$(TEST_COMPOSE) build gotest webtest; \
	$(TEST_COMPOSE) run --rm gotest; \
	$(TEST_COMPOSE) run --rm webtest

test-host: ## Same Go tests on the host against the compose Postgres (fast loop; needs `make up`)
	TEST_DATABASE_URL=$(HOST_TEST_DB) LLM_PROVIDER=mock ANTHROPIC_API_KEY= go test -race -count=1 ./...
	cd web && npm test

lint: ## golangci-lint + vue-tsc
	golangci-lint run ./...
	cd web && npm run typecheck

fmt: ## gofmt the Go code
	gofmt -w .

schema: ## Regenerate docs/contracts/*.wire.schema.json from internal/catalog
	go run ./cmd/api schema docs/contracts

dev-sub: ## Compose dependencies + API on the host with the claudecode adapter (owner's subscription)
	LLM_PROVIDER=mock $(COMPOSE) up -d --build --wait postgres mockprovider worker
	-$(COMPOSE) stop api
	DATABASE_URL=postgres://scc:scc@localhost:55432/scc?sslmode=disable PROVIDER_URL=http://localhost:8090 \
	  LLM_PROVIDER=claudecode ANTHROPIC_API_KEY= go run ./cmd/api serve

stress: ## Stress scenarios S1–S7 -> docs/stress/REPORT.md (mock LLM)
	@echo "stress: not implemented yet (P1, delivery builder)"

e2e: ## Playwright end-to-end against the compose stack (mock LLM)
	@echo "e2e: not implemented yet (P1, web builder)"

eval: ## LLM eval; PROVIDER=mock|claudecode|anthropic (anthropic also needs CONFIRM_SPEND=yes)
	@if [ -z "$(PROVIDER)" ]; then echo "eval: set PROVIDER=mock|claudecode|anthropic (there is no auto here)"; exit 2; fi
	@if [ "$(PROVIDER)" = "anthropic" ] && [ "$(CONFIRM_SPEND)" != "yes" ]; then \
	  echo "eval: PROVIDER=anthropic calls the paid Claude API. Owner only: make eval PROVIDER=anthropic CONFIRM_SPEND=yes"; exit 2; fi
	@echo "eval: not implemented yet (P1, llm builder)"

smoke-live: ## One real Claude API call per task (owner only; needs CONFIRM_SPEND=yes and a key in .env)
	@if [ "$(CONFIRM_SPEND)" != "yes" ]; then \
	  echo "smoke-live: calls the paid Claude API. Owner only: make smoke-live CONFIRM_SPEND=yes"; exit 2; fi
	@echo "smoke-live: not implemented yet (llm builder)"

sanitize-check: ## gitleaks over git history and ai-sessions/, plus the owner-term grep
	scripts/sanitize-check.sh

fresh-clone-check: ## Clean clone -> compose up (alternate ports) -> healthy -> seed present -> make test
	scripts/fresh-clone-check.sh

harvest-sessions: ## Copy this project's Claude Code transcripts into ai-sessions/_raw/ (gitignored)
	scripts/harvest-sessions.sh
