.PHONY: dev prod-local db\:dev clean\:dev clean\:prod-local e2e e2e\:stack e2e\:install e2e\:ui clean\:e2e setup hooks

DEV_CONFIG := configs/octroi.dev.yaml
E2E_CONFIG := configs/octroi.e2e.yaml
DEV_ENV := configs/.env.dev
PROD_ENV := configs/.env.prod

DEV_PROJECT := octroi-dev
PROD_PROJECT := octroi-prod
E2E_PROJECT := octroi-e2e

# --- dev: Postgres in Docker, server via go run (port 8080) ---
dev:
	@set -a && . ./$(DEV_ENV) && set +a && \
		docker compose -p $(DEV_PROJECT) up -d --wait && \
		go run ./cmd/octroi migrate --config $(DEV_CONFIG) && \
		(go run ./cmd/octroi ensure-admin --config $(DEV_CONFIG) 2>/dev/null || true) && \
		OCTROI_DEV=1 go run ./cmd/octroi serve --config $(DEV_CONFIG)

# --- dev: start only Postgres (port 5433) ---
db\:dev:
	docker compose -p $(DEV_PROJECT) up -d --wait

# --- dev: tear down Postgres and volumes ---
clean\:dev:
	docker compose -p $(DEV_PROJECT) down -v

# --- prod-local: everything in Docker (port 9080) ---
prod-local:
	@set -a && . ./$(PROD_ENV) && set +a && \
		docker compose -p $(PROD_PROJECT) -f docker-compose.local.yml up -d --build --wait

# --- prod-local: tear down containers, volumes, and images ---
clean\:prod-local:
	docker compose -p $(PROD_PROJECT) -f docker-compose.local.yml down -v --rmi local

# --- e2e: isolated stack (Postgres :5435, server :9091) + Playwright ---
e2e:
	@set -a && . ./$(DEV_ENV) && set +a && \
		docker compose -p $(E2E_PROJECT) -f docker-compose.e2e.yml up -d --wait && \
		go run ./cmd/octroi migrate --config $(E2E_CONFIG) 2>/dev/null && \
		(go run ./cmd/octroi ensure-admin --config $(E2E_CONFIG) 2>/dev/null || true) && \
		(OCTROI_DEV=1 go run ./cmd/octroi serve --config $(E2E_CONFIG) &) && \
		sleep 3 && \
		(timeout 30 scripts/seed.sh http://localhost:9091 2>/dev/null || true) && \
		cd e2e && BASE_URL=http://local.localhost:9091 npm test; \
		STATUS=$$?; \
		kill %1 2>/dev/null || true; \
		docker compose -p $(E2E_PROJECT) -f docker-compose.e2e.yml down 2>/dev/null || true; \
		exit $$STATUS

e2e\:stack:
	@set -a && . ./$(DEV_ENV) && set +a && \
		docker compose -p $(E2E_PROJECT) -f docker-compose.e2e.yml up -d --wait && \
		go run ./cmd/octroi migrate --config $(E2E_CONFIG) 2>/dev/null && \
		(go run ./cmd/octroi ensure-admin --config $(E2E_CONFIG) 2>/dev/null || true) && \
		(OCTROI_DEV=1 go run ./cmd/octroi serve --config $(E2E_CONFIG) &) && \
		sleep 3 && \
		(timeout 30 scripts/seed.sh http://localhost:9091 2>/dev/null || true) && \
		echo "" && \
		echo "E2E stack ready (Postgres :5435, server :9091)" && \
		echo "Run tests:  cd e2e && BASE_URL=http://local.localhost:9091 npx playwright test" && \
		echo "Cleanup:    make clean:e2e" && \
		echo "Press Ctrl-C to stop the server." && \
		wait

e2e\:install:
	cd e2e && npm install && npx playwright install chromium

e2e\:ui:
	@set -a && . ./$(DEV_ENV) && set +a && \
		docker compose -p $(E2E_PROJECT) -f docker-compose.e2e.yml up -d --wait && \
		go run ./cmd/octroi migrate --config $(E2E_CONFIG) 2>/dev/null && \
		(go run ./cmd/octroi ensure-admin --config $(E2E_CONFIG) 2>/dev/null || true) && \
		(OCTROI_DEV=1 go run ./cmd/octroi serve --config $(E2E_CONFIG) &) && \
		sleep 3 && \
		(timeout 30 scripts/seed.sh http://localhost:9091 2>/dev/null || true) && \
		cd e2e && BASE_URL=http://local.localhost:9091 npx playwright test --ui; \
		STATUS=$$?; \
		kill %1 2>/dev/null || true; \
		docker compose -p $(E2E_PROJECT) -f docker-compose.e2e.yml down 2>/dev/null || true; \
		exit $$STATUS

clean\:e2e:
	docker compose -p $(E2E_PROJECT) -f docker-compose.e2e.yml down -v

# --- setup: one-time dev environment setup ---
setup:
	git config core.hooksPath .githooks
	cd e2e && npm install && npx playwright install chromium

# --- hooks: install git hooks from .githooks/ (included in setup) ---
hooks:
	git config core.hooksPath .githooks
