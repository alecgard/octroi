.PHONY: dev prod-local db\:dev clean\:dev clean\:prod-local

DEV_CONFIG := configs/octroi.dev.yaml
DEV_ENV := configs/.env.dev
PROD_ENV := configs/.env.prod

# --- dev: Postgres in Docker, server via go run (port 8080) ---
dev:
	@set -a && . ./$(DEV_ENV) && set +a && \
		docker compose up -d --wait && \
		go run ./cmd/octroi migrate --config $(DEV_CONFIG) && \
		(go run ./cmd/octroi ensure-admin --config $(DEV_CONFIG) 2>/dev/null || true) && \
		OCTROI_DEV=1 go run ./cmd/octroi serve --config $(DEV_CONFIG)

# --- dev: start only Postgres (port 5433) ---
db\:dev:
	docker compose up -d --wait

# --- dev: tear down Postgres and volumes ---
clean\:dev:
	docker compose down -v

# --- prod-local: everything in Docker (port 9080) ---
prod-local:
	@set -a && . ./$(PROD_ENV) && set +a && \
		docker compose -f docker-compose.local.yml up -d --build --wait

# --- prod-local: tear down containers, volumes, and images ---
clean\:prod-local:
	docker compose -f docker-compose.local.yml down -v --rmi local
