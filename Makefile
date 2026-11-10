# Smart Campaign Composer — developer entry points (KICKOFF §11).
# Money guard (need.md N-PLAN-014): up/test/e2e/stress/acceptance force LLM_PROVIDER=mock; only `smoke-live`,
# `eval PROVIDER=anthropic` and `acceptance PROVIDER=anthropic` can reach the paid API, and they refuse to run
# without CONFIRM_SPEND=yes.

SHELL        := /bin/bash
COMPOSE      := docker compose
TEST_COMPOSE := docker compose -p scc-test --profile test
HOST_TEST_DB ?= postgres://scc:scc@localhost:55432/postgres?sslmode=disable

.DEFAULT_GOAL := help
.PHONY: help up down logs ps test test-host lint fmt schema dev-sub stress e2e eval smoke-live acceptance \
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

stress: ## Stress S1-S3, S5-S7 against Postgres on 55432 -> docs/stress/REPORT.md, REPORT-api.md (needs `make up`)
	TEST_DATABASE_URL=$(HOST_TEST_DB) LLM_PROVIDER=mock ANTHROPIC_API_KEY= STRESS=1 go test -tags stress -count=1 -run TestStress -timeout 60m -v ./cmd/worker/
	TEST_DATABASE_URL=$(HOST_TEST_DB) LLM_PROVIDER=mock ANTHROPIC_API_KEY= STRESS=1 go test -tags stress -count=1 -run TestStressAPI -timeout 30m -v ./cmd/api/

e2e: up ## Playwright end-to-end against the compose stack (mock LLM); report in web/playwright-report
	LLM_PROVIDER=mock $(COMPOSE) --profile e2e build e2e
	LLM_PROVIDER=mock $(COMPOSE) --profile e2e run --rm e2e

eval: ## LLM eval; PROVIDER=mock|claudecode|anthropic (anthropic also needs CONFIRM_SPEND=yes)
	@if [ -z "$(PROVIDER)" ]; then echo "eval: set PROVIDER=mock|claudecode|anthropic (there is no auto here)"; exit 2; fi
	@if [ "$(PROVIDER)" = "anthropic" ] && [ "$(CONFIRM_SPEND)" != "yes" ]; then \
	  echo "eval: PROVIDER=anthropic calls the paid Claude API. Owner only: make eval PROVIDER=anthropic CONFIRM_SPEND=yes"; exit 2; fi
	@# The anthropic path reads ANTHROPIC_API_KEY from the owner's shell environment; make never loads .env.
	EVAL_PROVIDER=$(PROVIDER) CONFIRM_SPEND=$(CONFIRM_SPEND) TEST_DATABASE_URL=$(HOST_TEST_DB) \
	  go test -count=1 -v -run TestEvalReport ./internal/llm/eval/

smoke-live: ## One real Claude API call per task (needs CONFIRM_SPEND=yes and ANTHROPIC_API_KEY in your shell)
	@if [ "$(CONFIRM_SPEND)" != "yes" ]; then \
	  echo "smoke-live: calls the paid Claude API. Owner only: make smoke-live CONFIRM_SPEND=yes"; exit 2; fi
	@# Reads ANTHROPIC_API_KEY from the owner's shell (e.g. `set -a; . ./.env; set +a` first); the test refuses
	@# without the flag and skips cleanly without a key.
	CONFIRM_SPEND=$(CONFIRM_SPEND) go test -tags smokelive -count=1 -v -run TestSmokeLive ./internal/llm/anthropic/

acceptance: ## Acceptance A1-A13 through the HTTP API (mock LLM; PROVIDER=anthropic also needs CONFIRM_SPEND=yes; needs `make up`)
	@if [ -n "$(PROVIDER)" ] && [ "$(PROVIDER)" != "mock" ] && [ "$(PROVIDER)" != "anthropic" ]; then \
	  echo "acceptance: PROVIDER=mock (default) or anthropic"; exit 2; fi
	@if [ "$(PROVIDER)" = "anthropic" ] && [ "$(CONFIRM_SPEND)" != "yes" ]; then \
	  echo "acceptance: PROVIDER=anthropic calls the paid Claude API. Owner only: make acceptance PROVIDER=anthropic CONFIRM_SPEND=yes"; exit 2; fi
	@# Live mode reads ANTHROPIC_API_KEY from the owner's shell; make never loads .env. The suite stops when the
	@# cumulative LLM cost passes ACCEPTANCE_MAX_USD (default 1.00).
	@mkdir -p docs/acceptance
	$(if $(filter anthropic,$(PROVIDER)),LLM_PROVIDER=anthropic CONFIRM_SPEND=$(CONFIRM_SPEND),LLM_PROVIDER=mock ANTHROPIC_API_KEY=) \
	  TEST_DATABASE_URL=$(HOST_TEST_DB) ACCEPTANCE_REPORT=docs/acceptance \
	  go test -tags acceptance -count=1 -v -timeout 20m -run TestAcceptance ./acceptance/

sanitize-check: ## gitleaks over git history and ai-sessions/, plus the owner-term grep
	scripts/sanitize-check.sh

fresh-clone-check: ## Clean clone -> compose up (alternate ports, no .env) -> healthy -> seed present -> make test
	scripts/fresh-clone-check.sh --with-tests

harvest-sessions: ## Copy this project's Claude Code transcripts into ai-sessions/_raw/ (gitignored)
	scripts/harvest-sessions.sh
