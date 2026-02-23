.PHONY: dev prod-local db clean\:dev clean\:prod-local

DEV_CONFIG := configs/octroi.dev.yaml
DEV_ENV := configs/.env.dev
PROD_CONFIG := configs/octroi.prod.yaml
PROD_ENV := configs/.env.prod
BIN := octroi

# --- Dev: start Postgres, run migrations, ensure admin, serve with go run ---
dev:
	@set -a && . ./$(DEV_ENV) && set +a && \
		docker compose up -d --wait && \
		go run ./cmd/octroi migrate --config $(DEV_CONFIG) && \
		(go run ./cmd/octroi ensure-admin --config $(DEV_CONFIG) 2>/dev/null || true) && \
		OCTROI_DEV=1 go run ./cmd/octroi serve --config $(DEV_CONFIG)

# --- Prod-local: build binary, start local Postgres, run migrations, serve ---
prod-local: $(BIN)
	@set -a && . ./$(PROD_ENV) && set +a && \
		OCTROI_DATABASE_URL=$${OCTROI_DATABASE_URL:-postgres://octroi:$${POSTGRES_PASSWORD}@localhost:5434/octroi?sslmode=disable} && \
		export OCTROI_DATABASE_URL && \
		docker compose -f docker-compose.local.yml up -d --wait && \
		./$(BIN) migrate --config $(PROD_CONFIG) && \
		(./$(BIN) ensure-admin --config $(PROD_CONFIG) 2>/dev/null || true) && \
		./$(BIN) serve --config $(PROD_CONFIG)

# --- Local Postgres via Docker (dev) ---
db:
	docker compose up -d --wait

# --- Local Postgres via Docker (prod) ---
db\:prod:
	docker compose -f docker-compose.local.yml up -d --wait

$(BIN):
	CGO_ENABLED=0 go build -o $(BIN) ./cmd/octroi

clean\:dev:
	docker compose down -v

clean\:prod-local:
	rm -f $(BIN)
	docker compose -f docker-compose.local.yml down -v
